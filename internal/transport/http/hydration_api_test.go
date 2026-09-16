package http_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

func serveAgua(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Hydration:   &handlers.Hydration{Store: repo.NewHydrationRepo(repo.NewTxManager(pool))},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

func diasDeAgua(t *testing.T, h http.Handler, query string) []struct {
	Day string `json:"day"`
	Ml  int    `json:"ml"`
} {
	t.Helper()
	w := get(t, h, "/v1/hydration"+query)
	if w.Code != http.StatusOK {
		t.Fatalf("ler água: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Days []struct {
			Day string `json:"day"`
			Ml  int    `json:"ml"`
		} `json:"days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Days
}

// A água bebida sobrevive — era a metade que não saía do telemóvel.
func TestAguaGravadaVolta(t *testing.T) {
	h := serveAgua(t)

	if w := put(t, h, "/v1/hydration/2026-09-16", `{"ml":1750}`); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}
	dias := diasDeAgua(t, h, "?from=2026-09-01&to=2026-09-30")
	if len(dias) != 1 || dias[0].Ml != 1750 || dias[0].Day != "2026-09-16" {
		t.Fatalf("devolvido: %+v", dias)
	}
}

/*
 * Mandar o total duas vezes é o mesmo total.
 *
 * O corpo leva o **total**, não um acréscimo. Com "+250", uma rede lenta que
 * repetisse o pedido fazia a pessoa beber meio litro sem levantar o copo.
 */
func TestAguaEOTotalDoDiaENaoUmAcrescimo(t *testing.T) {
	h := serveAgua(t)

	for i := 0; i < 3; i++ {
		if w := put(t, h, "/v1/hydration/2026-09-16", `{"ml":1000}`); w.Code != http.StatusOK {
			t.Fatalf("gravar %d: %d", i, w.Code)
		}
	}
	dias := diasDeAgua(t, h, "?from=2026-09-16&to=2026-09-16")
	if len(dias) != 1 || dias[0].Ml != 1000 {
		t.Fatalf("três envios do mesmo total deram %+v", dias)
	}

	// Um total novo substitui: quem bebeu mais tem mais, não outra linha.
	if w := put(t, h, "/v1/hydration/2026-09-16", `{"ml":1500}`); w.Code != http.StatusOK {
		t.Fatalf("actualizar: %d", w.Code)
	}
	dias = diasDeAgua(t, h, "?from=2026-09-16&to=2026-09-16")
	if len(dias) != 1 || dias[0].Ml != 1500 {
		t.Fatalf("depois de actualizar: %+v", dias)
	}
}

// Cada dia é um dia: a água de ontem não se mistura com a de hoje.
func TestAguaESeparadaPorDia(t *testing.T) {
	h := serveAgua(t)
	for dia, ml := range map[string]string{
		"2026-09-14": `{"ml":2000}`,
		"2026-09-15": `{"ml":900}`,
		"2026-09-16": `{"ml":1250}`,
	} {
		if w := put(t, h, "/v1/hydration/"+dia, ml); w.Code != http.StatusOK {
			t.Fatalf("%s: %d", dia, w.Code)
		}
	}
	dias := diasDeAgua(t, h, "?from=2026-09-01&to=2026-09-30")
	if len(dias) != 3 {
		t.Fatalf("%d dias, esperava 3", len(dias))
	}
	// Do mais recente para o mais antigo.
	if dias[0].Day != "2026-09-16" || dias[2].Day != "2026-09-14" {
		t.Errorf("ordem: %s … %s", dias[0].Day, dias[2].Day)
	}
}

// Um valor impossível é recusado com o campo, não guardado.
func TestAguaImpossivelERecusada(t *testing.T) {
	h := serveAgua(t)
	for _, corpo := range []string{`{"ml":-100}`, `{"ml":90000}`} {
		w := put(t, h, "/v1/hydration/2026-09-16", corpo)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s devia ser recusado, deu %d", corpo, w.Code)
		}
	}
	if dias := diasDeAgua(t, h, "?from=2026-09-16&to=2026-09-16"); len(dias) != 0 {
		t.Errorf("guardou na mesma: %+v", dias)
	}
}
