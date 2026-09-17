package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

/*
 * A avaliação do ciclo, pela rota.
 *
 * ⚠️ Esta decisão **só existia no telemóvel**: `assessCycle` corria dentro do
 * `NutritionStrategyCard` e não tinha gémeo em Go. A única máquina capaz de
 * dizer "o teu alvo está errado" era o aparelho de quem estivesse a olhar para
 * o ecrã — quem trocasse de telemóvel perdia a avaliação.
 */

type avaliacaoJSON struct {
	Cycle struct {
		ID           string `json:"id"`
		StartDateISO string `json:"startDateISO"`
		EndDateISO   string `json:"endDateISO"`
		Status       string `json:"status"`
	} `json:"cycle"`
	Assessment struct {
		Headline     string `json:"headline"`
		Response     string `json:"response"`
		Confidence   string `json:"confidence"`
		ObservedTdee *int   `json:"observedTdee"`
		Signals      []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
		} `json:"signals"`
		Adherence struct {
			Score      float64 `json:"score"`
			LoggedDays int     `json:"loggedDays"`
			Evaluable  bool    `json:"evaluable"`
		} `json:"adherence"`
	} `json:"assessment"`
	Adaptation struct {
		Kind         string `json:"kind"`
		Reason       string `json:"reason"`
		CalorieDelta int    `json:"calorieDelta"`
		Applied      bool   `json:"applied"`
	} `json:"adaptation"`
}

func avaliacao(t *testing.T, h http.Handler, dia string) avaliacaoJSON {
	t.Helper()
	w := get(t, h, "/v1/nutrition/assessment?localDay="+dia)
	if w.Code != http.StatusOK {
		t.Fatalf("avaliação: %d — %s", w.Code, w.Body.String())
	}
	var out avaliacaoJSON
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("resposta ilegível: %v — %s", err, w.Body.String())
	}
	return out
}

/*
 * Sem registos não há conclusão — e dizê-lo é a conclusão certa.
 *
 * Um número tirado de três dias de registo é pior do que nenhum: parece
 * informação. Por isso a falta de dados tem uma saída própria, `await_data`, e
 * não um palpite mais fraco.
 */
func TestSemRegistosAAvaliacaoEsperaPorDados(t *testing.T) {
	h := serveCompras(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	if w := post(t, h, "/v1/goals", fixedBody, nil); w.Code != http.StatusCreated {
		t.Fatalf("objectivo: %d — %s", w.Code, w.Body.String())
	}

	a := avaliacao(t, h, quarta)
	if a.Assessment.Confidence != "low" {
		t.Errorf("confiança sem dados: %q", a.Assessment.Confidence)
	}
	if a.Adaptation.Kind != "await_data" {
		t.Errorf("proposta sem dados: %q — %s", a.Adaptation.Kind, a.Adaptation.Reason)
	}
	if a.Adaptation.CalorieDelta != 0 {
		t.Errorf("propôs mexer no alvo sem dados: %d", a.Adaptation.CalorieDelta)
	}
	if a.Cycle.StartDateISO == "" || a.Cycle.EndDateISO == "" || a.Cycle.Status != "active" {
		t.Errorf("ciclo incompleto: %+v", a.Cycle)
	}
}

/*
 * Com registos a sério, a avaliação diz alguma coisa.
 *
 * Catorze dias de refeições registadas, abaixo do alvo de proteína: é o caso
 * mais comum e o primeiro ajuste que o motor propõe, porque apertar calorias a
 * quem não chega à proteína é apertar o que não é o problema.
 */
func TestComRegistosAAvaliacaoConclui(t *testing.T) {
	h := serveCompras(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	if w := post(t, h, "/v1/goals", fixedBody, nil); w.Code != http.StatusCreated {
		t.Fatalf("objectivo: %d — %s", w.Code, w.Body.String())
	}

	/*
	 * Os registos contam a partir do **início do ciclo**, e o ciclo começa
	 * quando a estratégia entrou em vigor. Escrever catorze dias antes disso
	 * era escrever catorze dias que a avaliação não olha — e foi o que
	 * aconteceu à primeira versão deste teste, que via 4 de 14.
	 */
	inicioDoCiclo := avaliacao(t, h, quarta).Cycle.StartDateISO
	base, err := time.Parse("2006-01-02", inicioDoCiclo)
	if err != nil {
		t.Fatalf("início do ciclo ilegível (%q): %v", inicioDoCiclo, err)
	}
	base = base.Add(12 * time.Hour)
	for d := 0; d < 14; d++ {
		for m, slot := range []string{"breakfast", "lunch", "dinner"} {
			dia := base.AddDate(0, 0, d)
			corpo := fmt.Sprintf(`{
			  "slot":%q,"status":"eaten","portion":1,"kcal":700,
			  "macros":{"protein":18,"carbs":80,"fat":22},
			  "localDay":%q,"recordedAt":%q,"source":"plan"}`,
				slot, dia.Format("2006-01-02"),
				dia.Add(time.Duration(m)*time.Hour).Format(time.RFC3339))
			if w := put(t, h, "/v1/nutrition/logs/"+fmt.Sprintf("log_%d_%s", d, slot), corpo); w.Code != http.StatusOK {
				t.Fatalf("registo %d/%s: %d — %s", d, slot, w.Code, w.Body.String())
			}
		}
	}

	// Catorze dias depois do início, que é quando há o que avaliar.
	a := avaliacao(t, h, base.AddDate(0, 0, 14).Format("2006-01-02"))
	if a.Assessment.Adherence.LoggedDays < 10 {
		t.Fatalf("os registos não chegaram à avaliação: %d dias", a.Assessment.Adherence.LoggedDays)
	}
	if a.Assessment.Confidence == "low" {
		t.Errorf("com 14 dias a confiança ainda é baixa: %+v", a.Assessment)
	}
	if a.Assessment.Headline == "" {
		t.Error("avaliação sem frase para mostrar")
	}
	// A proposta sai **não aplicada**: um alvo que muda porque alguém abriu um
	// ecrã é um alvo em que não se confia.
	if a.Adaptation.Kind == "await_data" {
		t.Errorf("com 14 dias ainda diz que espera por dados: %s", a.Adaptation.Reason)
	}
	if a.Adaptation.Kind == "" || a.Adaptation.Reason == "" {
		t.Errorf("proposta sem conteúdo: %+v", a.Adaptation)
	}
}

// Sem objectivo não há estratégia em vigor — e isso é o princípio, não um erro.
func TestSemEstrategiaAAvaliacaoDiz404(t *testing.T) {
	h := serveCompras(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	if w := get(t, h, "/v1/nutrition/assessment?localDay="+quarta); w.Code != http.StatusNotFound {
		t.Errorf("sem objectivo devolveu %d — %s", w.Code, w.Body.String())
	}
}
