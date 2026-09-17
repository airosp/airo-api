package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func patch(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, path, bytes.NewBufferString(body))
	r.Header.Set("Authorization", "Bearer token-de-teste")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type objetivoJSON struct {
	Priority string `json:"priority"`
	Horizon  string `json:"horizon"`
	Journey  struct {
		TargetDate *string `json:"targetDate"`
	} `json:"journey"`
	Targets []struct {
		Metric   string  `json:"metric"`
		Value    float64 `json:"value"`
		Baseline float64 `json:"baseline"`
	} `json:"targets"`
}

func objetivoActivo(t *testing.T, h http.Handler) objetivoJSON {
	t.Helper()
	w := get(t, h, "/v1/goals/active")
	if w.Code != http.StatusOK {
		t.Fatalf("ler objectivo: %d — %s", w.Code, w.Body.String())
	}
	var o objetivoJSON
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

/*
 * Mudar o peso alvo não deita a jornada fora.
 *
 * ⚠️ Era a única saída que havia: o `POST` recusa um segundo objectivo activo,
 * por isso quem quisesse acertar 74 para 72 tinha de apagar o objectivo e
 * recomeçar — e com ele iam três meses de histórico.
 */
func TestMudarOPesoAlvoNaoDeitaAJornadaFora(t *testing.T) {
	h := serveComObjetivo(t)

	antes := objetivoActivo(t, h)
	var baseline float64
	for _, alvo := range antes.Targets {
		if alvo.Metric == "body_weight" {
			baseline = alvo.Baseline
		}
	}

	w := patch(t, h, "/v1/goals/active", `{"targetWeightKg":72}`)
	if w.Code != http.StatusOK {
		t.Fatalf("alterar: %d — %s", w.Code, w.Body.String())
	}

	depois := objetivoActivo(t, h)
	var novo, baselineDepois float64
	for _, alvo := range depois.Targets {
		if alvo.Metric == "body_weight" {
			novo, baselineDepois = alvo.Value, alvo.Baseline
		}
	}
	if novo != 72 {
		t.Errorf("peso alvo ficou %v, esperava 72", novo)
	}
	// O ponto de partida não se toca: reescrevê-lo apagava o progresso feito.
	if baselineDepois != baseline {
		t.Errorf("o ponto de partida mudou de %v para %v", baseline, baselineDepois)
	}
}

/*
 * Adiar a data é mudar um número; apagá-la seria mudar a jornada.
 *
 * Com data há fases e não há ciclos; sem data é ao contrário. A restrição
 * `journey_horizon_dates` diz isso em voz alta, e por isso trocar de horizonte
 * não passa por aqui — passa por começar outra jornada, com a antiga guardada.
 */
func TestAdiarADataMudaONumeroENaoAJornada(t *testing.T) {
	h := serveComObjetivo(t)

	if w := patch(t, h, "/v1/goals/active", `{"targetDate":"2027-03-01"}`); w.Code != http.StatusOK {
		t.Fatalf("adiar: %d — %s", w.Code, w.Body.String())
	}
	o := objetivoActivo(t, h)
	if o.Horizon != "fixed" {
		t.Errorf("horizonte ficou %q", o.Horizon)
	}
	if o.Journey.TargetDate == nil || *o.Journey.TargetDate == "" {
		t.Fatal("a data desapareceu")
	}
	if (*o.Journey.TargetDate)[:10] != "2027-03-01" {
		t.Errorf("data ficou %q", *o.Journey.TargetDate)
	}
}

// Um campo ausente é "não mexer", não "apaga".
func TestUmCampoAusenteNaoApagaNada(t *testing.T) {
	h := serveComObjetivo(t)
	antes := objetivoActivo(t, h)

	if w := patch(t, h, "/v1/goals/active", `{"priority":"health"}`); w.Code != http.StatusOK {
		t.Fatalf("%d — %s", w.Code, w.Body.String())
	}
	depois := objetivoActivo(t, h)

	if depois.Priority != "health" {
		t.Errorf("prioridade ficou %q", depois.Priority)
	}
	if (antes.Journey.TargetDate == nil) != (depois.Journey.TargetDate == nil) {
		t.Error("a data mudou sem ninguém lhe tocar")
	}
	if len(antes.Targets) > 0 && len(depois.Targets) > 0 &&
		antes.Targets[0].Value != depois.Targets[0].Value {
		t.Error("o alvo mudou sem ninguém lhe tocar")
	}
}

// Sem objectivo em vigor, 404 — é uma resposta, não uma avaria.
func TestAlterarSemObjetivoDa404(t *testing.T) {
	h, _, _ := serve(t)
	if w := patch(t, h, "/v1/goals/active", `{"targetWeightKg":72}`); w.Code != http.StatusNotFound {
		t.Fatalf("deu %d, esperava 404", w.Code)
	}
}

// Valores impossíveis são recusados com o campo.
func TestPesoAlvoImpossivelERecusado(t *testing.T) {
	h := serveComObjetivo(t)
	for _, corpo := range []string{`{"targetWeightKg":3}`, `{"targetWeightKg":900}`, `{"priority":"felicidade"}`} {
		w := patch(t, h, "/v1/goals/active", corpo)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s devia ser recusado, deu %d", corpo, w.Code)
		}
	}
}

// serveComObjetivo devolve um router com um objectivo activo já criado — é o
// estado normal de quem passou pelo assistente.
func serveComObjetivo(t *testing.T) http.Handler {
	t.Helper()
	h, _, _ := serve(t)

	corpo := `{"type":"outcome","horizon":"fixed","direction":"lose_weight","priority":"weight",
	           "startDate":"2026-09-01","targetDate":"2026-12-01",
	           "targets":[{"metric":"body_weight","value":74,"unit":"kg","direction":"decrease"}]}`
	w := post(t, h, "/v1/goals", corpo, map[string]string{"Idempotency-Key": "obj-1"})
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("criar objectivo: %d — %s", w.Code, w.Body.String())
	}
	return h
}
