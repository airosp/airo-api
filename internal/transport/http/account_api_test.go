package http_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/migrations"
)

// Apagar a conta apaga **tudo** — e "tudo" inclui o número no trilho de
// auditoria.
//
// O esquema faz o grosso por cascata. O que não cascateia é o `auth_event`:
// `user_id` fica a nulo e o telefone ficava lá. Um número é um identificador, e
// guardá-lo depois de alguém pedir para ser esquecido não é auditoria — é uma
// cópia da pessoa.
func TestApagarContaNaoDeixaNada(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}

	const telefone = "+258849998877"
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ($1,'MZ') RETURNING id`,
		telefone).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	// Coisas espalhadas por tabelas diferentes, para a cascata ter o que apanhar.
	if _, err := pool.Exec(ctx,
		`INSERT INTO profile (user_id, display_name, height_cm, experience)
		 VALUES ($1,'Ana',165,'beginner')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO measurement (user_id, metric, value, unit, recorded_at, source)
		 VALUES ($1,'body_weight',62,'kg',now(),'manual')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO exercise_preference (user_id, exercise_id, kind)
		 VALUES ($1,'dips','pinned')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth_event (user_id, phone_e164, kind, device_id)
		 VALUES ($1,$2,'otp_verified','telemovel-da-ana')`, userID, telefone); err != nil {
		t.Fatal(err)
	}
	// Um evento do mesmo número **sem** dono: é o que fica de uma tentativa de
	// entrada antes de a conta existir.
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth_event (phone_e164, kind) VALUES ($1,'otp_requested')`, telefone); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	router := airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Account:     &handlers.Account{Service: service.NewAccountService(repo.NewAccountRepo(tx), nil)},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})

	r := httptest.NewRequest(http.MethodDelete, "/v1/account", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("apagar: %d — %s", w.Code, w.Body.String())
	}

	conta := func(query string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := conta(`SELECT count(*) FROM app_user WHERE id = $1`, userID); n != 0 {
		t.Errorf("a conta ficou: %d", n)
	}
	if n := conta(`SELECT count(*) FROM profile WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("o perfil ficou: %d", n)
	}
	if n := conta(`SELECT count(*) FROM measurement WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("as pesagens ficaram: %d", n)
	}
	if n := conta(`SELECT count(*) FROM exercise_preference WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("as preferências ficaram: %d", n)
	}
	// O que interessa: o número não pode estar em lado nenhum.
	if n := conta(`SELECT count(*) FROM auth_event WHERE phone_e164 = $1`, telefone); n != 0 {
		t.Errorf("o número ficou no trilho de auditoria em %d linhas", n)
	}
	if n := conta(`SELECT count(*) FROM auth_event WHERE device_id IS NOT NULL`); n != 0 {
		t.Errorf("o identificador do aparelho ficou em %d linhas", n)
	}
	// Mas as linhas **existem**, sem quem as identifique: é o que permite ver
	// abuso em agregado sem guardar a pessoa.
	if n := conta(`SELECT count(*) FROM auth_event`); n != 2 {
		t.Errorf("o trilho devia manter 2 linhas anónimas, tem %d", n)
	}
}

// Apagar duas vezes é apagar uma. Responder 404 à segunda punha quem pediu a
// duvidar se a primeira funcionou.
func TestApagarContaDuasVezes(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, _ := airopg.Load(migrations.FS)
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258847776655','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	router := airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Account:     &handlers.Account{Service: service.NewAccountService(repo.NewAccountRepo(tx), nil)},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})

	for i := 1; i <= 2; i++ {
		r := httptest.NewRequest(http.MethodDelete, "/v1/account", nil)
		r.Header.Set("Authorization", "Bearer token-de-teste")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Errorf("tentativa %d: %d — %s", i, w.Code, w.Body.String())
		}
	}
}
