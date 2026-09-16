package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// del não existia: as rotas de apagar que havia eram todas de caminho fixo.
func del(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, path, nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func serveDespensa(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Pantry:      &handlers.Pantry{Store: repo.NewPantryRepo(repo.NewTxManager(pool))},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

// O alimento que a pessoa escreveu volta com as gramas que ela escreveu.
//
// É o dado que custa mais a reintroduzir: ninguém se lembra dos macros da
// receita que escreveu há três meses.
func TestAlimentoProprioVoltaComOsMacros(t *testing.T) {
	h := serveDespensa(t)

	corpo := `{"name":"Matapa da avó","kcal":320,"macros":{"protein":18,"carbs":22,"fat":16},"serving":250}`
	if w := put(t, h, "/v1/nutrition/custom-foods/custom_1", corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	w := get(t, h, "/v1/nutrition/custom-foods")
	var body struct {
		Foods []struct {
			ID     string  `json:"id"`
			Name   string  `json:"name"`
			Kcal   float64 `json:"kcal"`
			Macros struct {
				Protein float64 `json:"protein"`
				Carbs   float64 `json:"carbs"`
				Fat     float64 `json:"fat"`
			} `json:"macros"`
			Serving int `json:"serving"`
		} `json:"foods"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Foods) != 1 {
		t.Fatalf("%d alimentos, esperava 1", len(body.Foods))
	}
	f := body.Foods[0]
	if f.Name != "Matapa da avó" || f.Kcal != 320 || f.Macros.Protein != 18 || f.Serving != 250 {
		t.Errorf("devolvido: %+v", f)
	}

	// Reenviar o mesmo identificador actualiza; não cria um segundo.
	if w := put(t, h, "/v1/nutrition/custom-foods/custom_1",
		`{"name":"Matapa","kcal":300,"macros":{"protein":18,"carbs":20,"fat":15},"serving":250}`); w.Code != http.StatusOK {
		t.Fatalf("actualizar: %d", w.Code)
	}
	w = get(t, h, "/v1/nutrition/custom-foods")
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Foods) != 1 || body.Foods[0].Name != "Matapa" {
		t.Fatalf("depois de actualizar: %+v", body.Foods)
	}

	// E apagar apaga.
	if w := del(t, h, "/v1/nutrition/custom-foods/custom_1"); w.Code != http.StatusNoContent {
		t.Fatalf("apagar: %d", w.Code)
	}
	w = get(t, h, "/v1/nutrition/custom-foods")
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Foods) != 0 {
		t.Errorf("ficou: %+v", body.Foods)
	}
}

// A favorita guarda a combinação, e devolve-a tal como foi guardada.
func TestFavoritaGuardaACombinacao(t *testing.T) {
	h := serveDespensa(t)

	corpo := `{"slot":"lunch","title":"Arroz com feijão","kcal":610,
	           "items":[{"foodId":"rice","grams":150},{"foodId":"beans","grams":120}],
	           "savedAt":"2026-09-16T12:00:00Z"}`
	if w := put(t, h, "/v1/nutrition/favourites/fav_1", corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	w := get(t, h, "/v1/nutrition/favourites")
	var body struct {
		Favourites []struct {
			ID    string `json:"id"`
			Slot  string `json:"slot"`
			Title string `json:"title"`
			Kcal  int    `json:"kcal"`
			Items []struct {
				FoodID string `json:"foodId"`
				Grams  int    `json:"grams"`
			} `json:"items"`
		} `json:"favourites"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Favourites) != 1 {
		t.Fatalf("%d favoritas, esperava 1", len(body.Favourites))
	}
	f := body.Favourites[0]
	if f.Slot != "lunch" || f.Title != "Arroz com feijão" || len(f.Items) != 2 {
		t.Errorf("devolvida: %+v", f)
	}
	if f.Items[0].FoodID != "rice" || f.Items[0].Grams != 150 {
		t.Errorf("itens perderam-se: %+v", f.Items)
	}
}

// Uma favorita sem alimentos, ou numa refeição que não existe, é recusada.
func TestFavoritaInvalidaERecusada(t *testing.T) {
	h := serveDespensa(t)
	for _, corpo := range []string{
		`{"slot":"almoco","title":"x","items":[{"foodId":"rice"}]}`,
		`{"slot":"lunch","title":"x"}`,
	} {
		if w := put(t, h, "/v1/nutrition/favourites/fav_x", corpo); w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s devia ser recusado, deu %d", corpo, w.Code)
		}
	}
}
