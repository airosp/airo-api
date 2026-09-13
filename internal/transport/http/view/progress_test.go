package view

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/journey"
)

// As duas formas do retrato são **diferentes**, e essa é a decisão.
//
// Devolver `forecast: null` num horizonte aberto convidaria a interface a
// mostrar um espaço vazio onde devia estar consistência. Sucesso em modo
// contínuo é consistência × progressão × recuperação, não aproximação a um
// número — e a resposta tem de dizer isso pela forma que tem.
func TestHorizonteFechadoEAbertoTemFormasDiferentes(t *testing.T) {
	quando := time.Date(2026, 11, 29, 0, 0, 0, 0, time.UTC)
	ritmo := -0.4
	tend := &journey.Trend{
		RollingAverage: 78.4, RatePerWeek: &ritmo, Samples: 20,
		SpanDays: 63, Confidence: journey.High,
	}
	adesao := journey.Adherence{Score: 0.96, Frequency: 0.96, Evaluable: true}

	fechado := BuildProgressSnapshot(SnapshotData{
		Horizon: "fixed", MetricKey: "body_weight", Trend: tend, Adherence: adesao,
		TemPrevisao: true, ForecastOn: &quando, ForecastConfidence: "high",
	})
	if fechado.Forecast == nil {
		t.Error("horizonte fechado sem previsão")
	}
	if fechado.Cycle != nil || fechado.Consistency != nil {
		t.Error("horizonte fechado não devia ter ciclo nem consistência")
	}

	revisao := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	aberto := BuildProgressSnapshot(SnapshotData{
		Horizon: "open_ended", MetricKey: "body_weight", Trend: tend, Adherence: adesao,
		CycleIndex: 3, CycleReview: &revisao,
		TemConsistencia: true, SessionsPlanned: 36, SessionsDone: 27, ConsistencyRate: 0.75,
		VolumeTrend: "up",
	})
	if aberto.Forecast != nil {
		t.Error("horizonte aberto não prevê data nenhuma")
	}
	if aberto.Cycle == nil || aberto.Consistency == nil || aberto.Progression == nil {
		t.Error("horizonte aberto sem ciclo, consistência ou progressão")
	}

	// E o JSON não leva o campo sequer — `omitempty` e não `null`.
	bruto, _ := json.Marshal(aberto)
	var m map[string]any
	json.Unmarshal(bruto, &m)
	if _, tem := m["forecast"]; tem {
		t.Error("o JSON do horizonte aberto não pode trazer 'forecast', nem a null")
	}
}

// Os números são acompanhados de uma frase. Um ritmo de -0,4 não diz nada a
// quem o lê; "a descer 0,40 kg por semana" diz.
func TestONumeroVemComFrase(t *testing.T) {
	casos := []struct {
		ritmo *float64
		quer  string
	}{
		{ptr(-0.4), "A descer 0,40 kg por semana."},
		{ptr(0.35), "A subir 0,35 kg por semana."},
		{ptr(0.0), "Estável nas últimas semanas."},
		{nil, "Ainda sem ritmo definido — faltam pesagens."},
	}
	for _, c := range casos {
		got := tendenciaPorPalavras(&journey.Trend{RatePerWeek: c.ritmo})
		if got != c.quer {
			t.Errorf("ritmo %v: %q ≠ %q", c.ritmo, got, c.quer)
		}
	}
}

// Sem data à vista **é** informação: o ritmo actual não leva ao alvo. Um espaço
// em branco dizia que a app não sabia.
func TestSemDataDizPorquePorPalavras(t *testing.T) {
	out := BuildProgressSnapshot(SnapshotData{
		Horizon: "fixed", TemPrevisao: true, ForecastConfidence: "medium",
	})
	if out.Forecast == nil || out.Forecast.ExpectedOn != nil {
		t.Fatal("esperava previsão sem data")
	}
	if out.Forecast.Headline != "Ao ritmo actual não há data à vista." {
		t.Errorf("frase: %q", out.Forecast.Headline)
	}
}

func ptr(v float64) *float64 { return &v }
