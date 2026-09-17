package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

func serveEspecialistas(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	especialistas := repo.NewSpecialistRepo(repo.NewTxManager(pool))
	if _, err := especialistas.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Specialists: &handlers.Specialists{Store: especialistas},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

type catalogoResposta struct {
	Specialists []struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Role      string   `json:"role"`
		RoleLabel string   `json:"roleLabel"`
		Gradient  []string `json:"gradient"`
		Invited   bool     `json:"invited"`
	} `json:"specialists"`
	Team []string `json:"team"`
}

func catalogo(t *testing.T, h http.Handler) catalogoResposta {
	t.Helper()
	w := get(t, h, "/v1/specialists")
	if w.Code != http.StatusOK {
		t.Fatalf("catálogo: %d — %s", w.Code, w.Body.String())
	}
	var c catalogoResposta
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// O catálogo chega pronto a desenhar, e diz quem já foi convidado.
func TestOCatalogoDizQuemJaFoiConvidado(t *testing.T) {
	h := serveEspecialistas(t)

	c := catalogo(t, h)
	if len(c.Specialists) != 8 {
		t.Fatalf("%d especialistas, esperava 8", len(c.Specialists))
	}
	if len(c.Team) != 0 {
		t.Errorf("equipa começou com %v", c.Team)
	}
	for _, s := range c.Specialists {
		if s.RoleLabel == "" || s.Name == "" || len(s.Gradient) != 2 {
			t.Errorf("%q: sem o que desenhar (%+v)", s.ID, s)
		}
		if s.Invited {
			t.Errorf("%q dá-se por convidado sem ninguém o ter convidado", s.ID)
		}
	}
}

/*
 * A equipa grava-se e volta — era o que não acontecia.
 *
 * A escolha vivia no telemóvel: reinstalar deixava a pessoa sozinha, com o
 * servidor a não saber que alguma vez tinha escolhido alguém.
 */
func TestAEquipaGravadaVolta(t *testing.T) {
	h := serveEspecialistas(t)

	w := put(t, h, "/v1/specialists/team", `{"team":["ana-silva","carla-mendes"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	c := catalogo(t, h)
	if len(c.Team) != 2 {
		t.Fatalf("equipa devolvida: %v", c.Team)
	}
	convidados := map[string]bool{}
	for _, s := range c.Specialists {
		if s.Invited {
			convidados[s.ID] = true
		}
	}
	if !convidados["ana-silva"] || !convidados["carla-mendes"] {
		t.Errorf("selos de convidado errados: %v", convidados)
	}
}

/*
 * Um por papel: trocar de treinador substitui, não acumula.
 *
 * Dois treinadores a assinar o mesmo plano é a confusão que o ecrã de escolha
 * existe para evitar — e a base garante-o em vez de o deixar à boa vontade do
 * cliente.
 */
func TestTrocarDeTreinadorSubstituiEmVezDeAcumular(t *testing.T) {
	h := serveEspecialistas(t)

	if w := put(t, h, "/v1/specialists/team", `{"team":["ana-silva"]}`); w.Code != http.StatusOK {
		t.Fatalf("primeiro: %d", w.Code)
	}
	if w := put(t, h, "/v1/specialists/team", `{"team":["miguel-torres"]}`); w.Code != http.StatusOK {
		t.Fatalf("segundo: %d", w.Code)
	}

	c := catalogo(t, h)
	if len(c.Team) != 1 || c.Team[0] != "miguel-torres" {
		t.Fatalf("equipa ficou %v, esperava só o Miguel", c.Team)
	}

	// Dois treinadores no mesmo pedido: fica o último.
	if w := put(t, h, "/v1/specialists/team", `{"team":["ana-silva","miguel-torres","carla-mendes"]}`); w.Code != http.StatusOK {
		t.Fatalf("terceiro: %d", w.Code)
	}
	c = catalogo(t, h)
	if len(c.Team) != 2 {
		t.Fatalf("equipa com %v — dois treinadores passaram", c.Team)
	}
}

// Dispensar toda a gente é um estado válido: o plano funciona sozinho.
func TestDispensarTodaAGenteEValido(t *testing.T) {
	h := serveEspecialistas(t)
	put(t, h, "/v1/specialists/team", `{"team":["ana-silva"]}`)

	if w := put(t, h, "/v1/specialists/team", `{"team":[]}`); w.Code != http.StatusOK {
		t.Fatalf("esvaziar: %d — %s", w.Code, w.Body.String())
	}
	if c := catalogo(t, h); len(c.Team) != 0 {
		t.Errorf("equipa ficou %v", c.Team)
	}
}
