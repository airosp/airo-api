package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"time"
)

type aulaJSON struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Specialist      string   `json:"specialist"`
	Focus           string   `json:"focus"`
	Level           string   `json:"level"`
	DurationSeconds int      `json:"durationSeconds"`
	Kcal            int      `json:"kcal"`
	VideoURL        string   `json:"videoUrl"`
	ThumbnailURL    string   `json:"thumbnailUrl"`
	Summary         string   `json:"summary"`
	Muscles         []string `json:"muscles"`
	Label           string   `json:"label"`
	DurationLabel   string   `json:"durationLabel"`
	FocusLabel      string   `json:"focusLabel"`
	LevelLabel      string   `json:"levelLabel"`
	EquipmentLabel  string   `json:"equipmentLabel"`
}

// serveCatalogoDeAulas monta o router com o catálogo de aulas carregado pelo
// seed — o mesmo que o deploy corre.
func serveCatalogoDeAulas(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)

	tx := repo.NewTxManager(pool)
	aulas := repo.NewClassRepo(tx)
	if _, err := aulas.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Classes:     &handlers.Classes{Store: aulas},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

// O catálogo chega ao cliente pronto a desenhar.
//
// O ecrã de explorar não sabe traduzir `mobility` nem dividir segundos por
// sessenta, e não é para saber: quem escreve a etiqueta é quem tem os dados.
// Este teste existe porque um campo em falta aqui é uma lista de cartões com o
// título certo e a legenda vazia.
func TestCatalogoDeAulasChegaProntoADesenhar(t *testing.T) {
	h := serveCatalogoDeAulas(t)

	w := get(t, h, "/v1/classes?all=1")
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Classes []aulaJSON `json:"classes"`
		Total   int        `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 15 || len(body.Classes) != 15 {
		t.Fatalf("o catálogo devolveu %d aulas, esperava 15", body.Total)
	}

	focos := map[string]int{}
	for _, a := range body.Classes {
		focos[a.Focus]++
		switch {
		case a.Title == "":
			t.Errorf("aula %q: sem título", a.ID)
		case a.VideoURL == "":
			t.Errorf("aula %q: sem vídeo", a.ID)
		case a.ThumbnailURL == "":
			t.Errorf("aula %q: sem imagem — o cartão fica cinzento", a.ID)
		case a.Summary == "":
			t.Errorf("aula %q: sem resumo", a.ID)
		case a.Label == "" || a.FocusLabel == "" || a.LevelLabel == "" || a.EquipmentLabel == "" || a.DurationLabel == "":
			t.Errorf("aula %q: etiqueta por escrever (%q/%q/%q/%q/%q)",
				a.ID, a.Label, a.FocusLabel, a.LevelLabel, a.EquipmentLabel, a.DurationLabel)
		case len(a.Muscles) == 0:
			t.Errorf("aula %q: sem músculos", a.ID)
		case a.Kcal <= 0:
			t.Errorf("aula %q: %d kcal", a.ID, a.Kcal)
		}

		// Nenhuma aula pode anunciar que não dura nada.
		if strings.Contains(a.Label, "· 0 ") || a.DurationLabel == "0 min" {
			t.Errorf("aula %q: %d s deu a etiqueta %q", a.ID, a.DurationSeconds, a.Label)
		}
		// A etiqueta do cartão acaba na duração — são a mesma coisa escrita uma
		// vez só.
		if !strings.HasSuffix(a.Label, a.DurationLabel) {
			t.Errorf("aula %q: %q não acaba em %q", a.ID, a.Label, a.DurationLabel)
		}
	}

	// Cinco focos, três aulas cada: sem isto, um dia de pernas não tinha aula
	// e o catálogo parecia cheio estando vazio para metade dos dias.
	for _, foco := range []string{"upper", "lower", "cardio", "full", "mobility"} {
		if focos[foco] != 3 {
			t.Errorf("foco %q: %d aulas, esperava 3", foco, focos[foco])
		}
	}
}

// Filtrar por foco devolve só esse foco — é o que os separadores do ecrã fazem.
func TestCatalogoFiltraPorFoco(t *testing.T) {
	h := serveCatalogoDeAulas(t)

	w := get(t, h, "/v1/classes?all=1&focus=mobility")
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Classes []aulaJSON `json:"classes"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Classes) != 3 {
		t.Fatalf("%d aulas de mobilidade, esperava 3", len(body.Classes))
	}
	for _, a := range body.Classes {
		if a.Focus != "mobility" {
			t.Errorf("aula %q tem foco %q numa lista de mobilidade", a.ID, a.Focus)
		}
		if a.FocusLabel != "Mobilidade" {
			t.Errorf("aula %q: etiqueta de foco %q", a.ID, a.FocusLabel)
		}
	}
}

// Uma aula abre pelo identificador, com o endereço do vídeo — é assim que o
// leitor a recebe.
func TestAulaDoCatalogoAbrePeloIdentificador(t *testing.T) {
	h := serveCatalogoDeAulas(t)

	w := get(t, h, "/v1/classes/mobilidade-da-manha")
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var a aulaJSON
	json.Unmarshal(w.Body.Bytes(), &a)
	if a.ID != "mobilidade-da-manha" || a.VideoURL == "" {
		t.Fatalf("aula devolvida: %+v", a)
	}
	if a.EquipmentLabel == "" {
		t.Error("sem etiqueta de equipamento: o ecrã mostra PRECISAS em branco")
	}
}
