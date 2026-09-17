package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// serveSessoes monta o router com dois aparelhos de sessão viva e um terceiro
// já expirado.
func serveSessoes(t *testing.T) (http.Handler, string) {
	t.Helper()
	_, pool, userID := serve(t)
	ctx := context.Background()

	for _, d := range []struct {
		id, plataforma, modelo, versao string
		expira                         string
	}{
		{"telefone-1", "ios", "iPhone 14", "1.2", "2026-12-01"},
		{"browser-1", "web", "", "", "2026-12-01"},
		{"telefone-velho", "android", "Pixel 5", "0.9", "2026-01-01"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO device (id, user_id, platform, model, app_version)
			 VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''))`,
			d.id, userID, d.plataforma, d.modelo, d.versao); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO refresh_token (user_id, device_id, token_hash, family_id, expires_at)
			 VALUES ($1,$2,digest($3,'sha256'),gen_random_uuid(),$4::date)`,
			userID, d.id, "token-"+d.id, d.expira); err != nil {
			// `digest` vem do pgcrypto; se não estiver, grava-se um hash à mão.
			if _, err2 := pool.Exec(ctx,
				`INSERT INTO refresh_token (user_id, device_id, token_hash, family_id, expires_at)
				 VALUES ($1,$2,$3::bytea,gen_random_uuid(),$4::date)`,
				userID, d.id, []byte("hash-"+d.id), d.expira); err2 != nil {
				t.Fatal(err2)
			}
		}
	}

	fixo := clock.NewFixed(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Sessions:    &handlers.Sessions{Devices: repo.NewAuthRepo(repo.NewTxManager(pool)), Clock: fixo},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), userID
}

type sessaoJSON struct {
	DeviceID string `json:"deviceId"`
	Platform string `json:"platform"`
	Label    string `json:"label"`
	LastSeen string `json:"lastSeen"`
}

func sessoes(t *testing.T, h http.Handler) []sessaoJSON {
	t.Helper()
	w := get(t, h, "/v1/auth/sessions")
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Sessions []sessaoJSON `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Sessions
}

/*
 * A lista mostra os aparelhos com sessão viva, e só esses.
 *
 * ⚠️ **Um aparelho, não um token.** A rotação cria um `refresh_token` novo a
 * cada renovação: listar tokens dava vinte linhas para o mesmo telemóvel, e
 * ninguém se reconhece numa lista dessas.
 */
func TestAListaMostraAparelhosVivos(t *testing.T) {
	h, _ := serveSessoes(t)

	lista := sessoes(t, h)
	if len(lista) != 2 {
		t.Fatalf("%d sessões, esperava 2 — a terceira está expirada: %+v", len(lista), lista)
	}
	porID := map[string]sessaoJSON{}
	for _, s := range lista {
		porID[s.DeviceID] = s
		if s.Label == "" {
			t.Errorf("%q sem etiqueta: o ecrã mostra um identificador de trinta caracteres", s.DeviceID)
		}
	}
	if porID["telefone-1"].Label != "iPhone 14 · v1.2" {
		t.Errorf("etiqueta do telefone: %q", porID["telefone-1"].Label)
	}
	if porID["browser-1"].Label != "Browser" {
		t.Errorf("etiqueta do browser: %q", porID["browser-1"].Label)
	}
	if _, expirado := porID["telefone-velho"]; expirado {
		t.Error("um aparelho com o token expirado apareceu como sessão viva")
	}
}

// Terminar tira o aparelho da lista — é o que "perdi o telemóvel" precisa.
func TestTerminarTiraOAparelhoDaLista(t *testing.T) {
	h, _ := serveSessoes(t)

	if w := del(t, h, "/v1/auth/sessions/telefone-1"); w.Code != http.StatusNoContent {
		t.Fatalf("terminar: %d — %s", w.Code, w.Body.String())
	}
	lista := sessoes(t, h)
	for _, s := range lista {
		if s.DeviceID == "telefone-1" {
			t.Fatal("o aparelho continua na lista depois de terminada a sessão")
		}
	}

	// Terminar outra vez é o mesmo resultado: já está terminada.
	if w := del(t, h, "/v1/auth/sessions/telefone-1"); w.Code != http.StatusNoContent {
		t.Errorf("segunda vez: %d", w.Code)
	}
}

/*
 * Ninguém termina a sessão de outra pessoa.
 *
 * O `userID` entra no `WHERE` da revogação: sem ele, adivinhar um `deviceId`
 * expulsava alguém da conta dele.
 */
func TestNinguemTerminaASessaoDeOutro(t *testing.T) {
	h, _ := serveSessoes(t)
	_, pool, outro := serve(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`INSERT INTO device (id, user_id, platform) VALUES ('alheio',$1,'ios')`, outro); err != nil {
		t.Skip("segundo utilizador indisponível nesta base")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO refresh_token (user_id, device_id, token_hash, family_id, expires_at)
		 VALUES ($1,'alheio',$2::bytea,gen_random_uuid(),'2026-12-01')`,
		outro, []byte("hash-alheio")); err != nil {
		t.Fatal(err)
	}

	// O pedido é feito pelo primeiro utilizador contra o aparelho do segundo.
	if w := del(t, h, "/v1/auth/sessions/alheio"); w.Code != http.StatusNoContent {
		t.Fatalf("%d", w.Code)
	}

	var vivos int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_token WHERE device_id = 'alheio' AND revoked_at IS NULL`).Scan(&vivos); err != nil {
		t.Fatal(err)
	}
	if vivos != 1 {
		t.Error("expulsou o aparelho de outra pessoa")
	}
}
