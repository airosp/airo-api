package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

func serveEdicoes(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:         fakeAuth{userID: userID},
		SessionEdits: &handlers.SessionEdits{Store: repo.NewSessionEditRepo(repo.NewTxManager(pool))},
		Idempotency:  middleware.NewMemoryStore(time.Hour),
	})
}

type diaEditadoJSON struct {
	Day   string `json:"day"`
	Edits struct {
		Removed []string `json:"removed"`
		Swapped map[string]struct {
			ExerciseID string `json:"exerciseId"`
		} `json:"swapped"`
		Tuned map[string]struct {
			Sets int `json:"sets"`
		} `json:"tuned"`
	} `json:"edits"`
}

func edicoes(t *testing.T, h http.Handler) []diaEditadoJSON {
	t.Helper()
	w := get(t, h, "/v1/training/day-edits")
	if w.Code != http.StatusOK {
		t.Fatalf("ler edições: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Days []diaEditadoJSON `json:"days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta ilegível: %v — %s", err, w.Body.String())
	}
	return body.Days
}

const hoje = "2026-09-16"

/*
 * O que a pessoa mudou no treino de hoje sobrevive a mudar de aparelho.
 *
 * Era esta a falha: tirar o agachamento porque o joelho dói ficava no telemóvel
 * e mais nada. Quem reinstalasse encontrava o agachamento de volta.
 */
func TestEdicoesDoDiaSobrevivem(t *testing.T) {
	h := serveEdicoes(t)

	corpo := `{"removed":["squat"],
	           "swapped":{"plank":{"exerciseId":"dead-bug","sets":3,"target":30,"restSeconds":45}},
	           "tuned":{"row":{"sets":4}}}`
	if w := put(t, h, "/v1/training/day-edits/"+hoje, corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	dias := edicoes(t, h)
	if len(dias) != 1 {
		t.Fatalf("esperava um dia, vieram %d", len(dias))
	}
	d := dias[0]
	if d.Day != hoje {
		t.Errorf("dia %q", d.Day)
	}
	if len(d.Edits.Removed) != 1 || d.Edits.Removed[0] != "squat" {
		t.Errorf("tirados: %v", d.Edits.Removed)
	}
	if d.Edits.Swapped["plank"].ExerciseID != "dead-bug" {
		t.Errorf("troca: %+v", d.Edits.Swapped)
	}
	if d.Edits.Tuned["row"].Sets != 4 {
		t.Errorf("afinação: %+v", d.Edits.Tuned)
	}
}

/*
 * Mandar duas vezes o mesmo dia dá o mesmo dia.
 *
 * O cliente manda o estado do ecrã. Uma rede que repete o pedido não pode
 * duplicar edições nem empilhar dias.
 */
func TestRegravarODiaNaoDuplica(t *testing.T) {
	h := serveEdicoes(t)

	for i := 0; i < 3; i++ {
		if w := put(t, h, "/v1/training/day-edits/"+hoje, `{"removed":["squat"]}`); w.Code != http.StatusOK {
			t.Fatalf("gravar %d: %d — %s", i, w.Code, w.Body.String())
		}
	}
	// A última escrita ganha, e continua a ser um dia só.
	if w := put(t, h, "/v1/training/day-edits/"+hoje, `{"removed":["squat","lunge"]}`); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	dias := edicoes(t, h)
	if len(dias) != 1 {
		t.Fatalf("esperava um dia, vieram %d", len(dias))
	}
	if len(dias[0].Edits.Removed) != 2 {
		t.Errorf("tirados: %v", dias[0].Edits.Removed)
	}
}

/*
 * Desfazer tudo é o mesmo que nunca ter mexido.
 *
 * Um conjunto vazio apaga a linha em vez de guardar um documento vazio: os dois
 * estados dão o mesmo treino, e ter duas formas de o dizer era pedir que alguém
 * as confundisse.
 */
func TestDesfazerTudoApaga(t *testing.T) {
	h := serveEdicoes(t)

	if w := put(t, h, "/v1/training/day-edits/"+hoje, `{"removed":["squat"]}`); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}
	if w := put(t, h, "/v1/training/day-edits/"+hoje,
		`{"removed":[],"added":[],"swapped":{},"tuned":{}}`); w.Code != http.StatusOK {
		t.Fatalf("desfazer: %d — %s", w.Code, w.Body.String())
	}

	if dias := edicoes(t, h); len(dias) != 0 {
		t.Fatalf("ficou lá qualquer coisa: %+v", dias)
	}
}

/*
 * Cada dia é o seu dia.
 *
 * Ao virar o dia, o treino volta ao que o motor propõe — o joelho de ontem não
 * manda no treino de amanhã. E os dias velhos deixam de vir.
 */
func TestCadaDiaEOSeuDia(t *testing.T) {
	h := serveEdicoes(t)

	ontem := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	agora := time.Now().UTC().Format("2006-01-02")
	antigo := time.Now().UTC().AddDate(0, 0, -40).Format("2006-01-02")

	for _, d := range []string{ontem, agora, antigo} {
		if w := put(t, h, "/v1/training/day-edits/"+d, `{"removed":["squat"]}`); w.Code != http.StatusOK {
			t.Fatalf("gravar %s: %d — %s", d, w.Code, w.Body.String())
		}
	}

	dias := edicoes(t, h)
	if len(dias) != 2 {
		t.Fatalf("esperava os dois dias recentes, vieram %d: %+v", len(dias), dias)
	}
	// Do mais recente para o mais antigo.
	if dias[0].Day != agora || dias[1].Day != ontem {
		t.Errorf("ordem: %s, %s", dias[0].Day, dias[1].Day)
	}
}

// Um dia que não é um dia não escreve nada.
func TestDiaInvalidoRecusado(t *testing.T) {
	h := serveEdicoes(t)
	for _, d := range []string{"ontem", "2026-13-40", "2026-09"} {
		if w := put(t, h, "/v1/training/day-edits/"+d, `{"removed":["squat"]}`); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q devolveu %d, esperava 422 — %s", d, w.Code, w.Body.String())
		}
	}
}

// Um documento grande de mais não entra na base de dados.
func TestEdicoesGrandesDeMaisRecusadas(t *testing.T) {
	h := serveEdicoes(t)

	gordo := "["
	for i := 0; i < 4000; i++ {
		if i > 0 {
			gordo += ","
		}
		gordo += fmt.Sprintf(`"exercicio-com-nome-comprido-%d"`, i)
	}
	gordo += "]"

	w := put(t, h, "/v1/training/day-edits/"+hoje, `{"removed":`+gordo+`}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("aceitou %d bytes com %d", len(gordo), w.Code)
	}
	if dias := edicoes(t, h); len(dias) != 0 {
		t.Errorf("gravou na mesma: %+v", dias)
	}
}

/*
 * Três semanas a trocar a mesma coisa deixa de ser uma troca.
 *
 * ⚠️ Quem tira o agachamento todas as semanas por causa de uma prótese repetia
 * a mesma decisão para sempre — e a app não aprendia nada com ela.
 */
func TestATrocaRepetidaViraHabito(t *testing.T) {
	h := serveEdicoes(t)

	// Três semanas, sempre a mesma troca.
	for _, d := range []int{-14, -7, 0} {
		dia := time.Now().UTC().AddDate(0, 0, d).Format("2006-01-02")
		corpo := `{"swapped":{"barbell_squat":{"exerciseId":"goblet_squat","sets":3,"target":10,"restSeconds":60}}}`
		if w := put(t, h, "/v1/training/day-edits/"+dia, corpo); w.Code != http.StatusOK {
			t.Fatalf("gravar %s: %d — %s", dia, w.Code, w.Body.String())
		}
	}

	w := get(t, h, "/v1/training/habits")
	if w.Code != http.StatusOK {
		t.Fatalf("ler hábitos: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Habits []struct {
			Kind  string `json:"kind"`
			From  string `json:"from"`
			To    string `json:"to"`
			Times int    `json:"times"`
			Since string `json:"since"`
		} `json:"habits"`
		Threshold int `json:"threshold"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta ilegível: %v — %s", err, w.Body.String())
	}
	if body.Threshold != 3 {
		t.Errorf("limiar %d", body.Threshold)
	}
	if len(body.Habits) != 1 {
		t.Fatalf("veio %+v", body.Habits)
	}
	got := body.Habits[0]
	if got.Kind != "swap" || got.From != "barbell_squat" || got.To != "goblet_squat" || got.Times != 3 {
		t.Errorf("%+v", got)
	}
}

// Duas vezes ainda não é um padrão, e perguntar antes disso é ruído.
func TestDuasVezesAindaNaoEHabito(t *testing.T) {
	h := serveEdicoes(t)

	for _, d := range []int{-7, 0} {
		dia := time.Now().UTC().AddDate(0, 0, d).Format("2006-01-02")
		if w := put(t, h, "/v1/training/day-edits/"+dia, `{"removed":["burpee"]}`); w.Code != http.StatusOK {
			t.Fatalf("gravar: %d", w.Code)
		}
	}

	w := get(t, h, "/v1/training/habits")
	var body struct {
		Habits []map[string]any `json:"habits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Habits) != 0 {
		t.Fatalf("veio %+v", body.Habits)
	}
}
