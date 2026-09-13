package journey

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Paridade da tendência: 120 casos.
//
// Cinco métricas (três ruidosas, duas não) × oito padrões de registo × três
// formas de evolução. Os padrões incluem os que quebram o caso simples: duas
// leituras só, três no mesmo instante, registos espaçados que deixam a janela
// recente vazia, e leituras todas fora da janela.
func TestParidadeComputeTrend(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts-trend.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var data struct {
		Cases []struct {
			In struct {
				Metric       string `json:"metric"`
				Measurements []struct {
					Metric     string  `json:"metric"`
					Value      float64 `json:"value"`
					RecordedAt string  `json:"recordedAtISO"`
				} `json:"measurements"`
				NowISO string `json:"nowISO"`
			} `json:"in"`
			Out *struct {
				RollingAverage  float64  `json:"rollingAverage"`
				PreviousAverage *float64 `json:"previousAverage"`
				RatePerWeek     *float64 `json:"ratePerWeek"`
				Samples         int      `json:"samples"`
				SpanDays        int      `json:"spanDays"`
				Confidence      string   `json:"confidence"`
			} `json:"out"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}

	cfg := DefaultConfig()
	for ci, c := range data.Cases {
		now, err := time.Parse(time.RFC3339, c.In.NowISO)
		if err != nil {
			t.Fatal(err)
		}
		ms := make([]Measurement, 0, len(c.In.Measurements))
		for _, m := range c.In.Measurements {
			at, err := time.Parse(time.RFC3339, m.RecordedAt)
			if err != nil {
				t.Fatal(err)
			}
			ms = append(ms, Measurement{Metric: m.Metric, Value: m.Value, RecordedAt: at})
		}

		got := ComputeTrend(cfg, c.In.Metric, ms, now)
		if (got == nil) != (c.Out == nil) {
			t.Fatalf("caso %d (%s): Go=%v TS=%v", ci, c.In.Metric, got != nil, c.Out != nil)
		}
		if got == nil {
			continue
		}

		if got.RollingAverage != c.Out.RollingAverage {
			t.Errorf("caso %d (%s): média %g ≠ %g", ci, c.In.Metric, got.RollingAverage, c.Out.RollingAverage)
		}
		if !mesmoPtr(got.PreviousAverage, c.Out.PreviousAverage) {
			t.Errorf("caso %d (%s): média anterior %v ≠ %v", ci, c.In.Metric, str(got.PreviousAverage), str(c.Out.PreviousAverage))
		}
		if !mesmoPtr(got.RatePerWeek, c.Out.RatePerWeek) {
			t.Errorf("caso %d (%s): ritmo %v ≠ %v", ci, c.In.Metric, str(got.RatePerWeek), str(c.Out.RatePerWeek))
		}
		if got.Samples != c.Out.Samples || got.SpanDays != c.Out.SpanDays {
			t.Errorf("caso %d (%s): %d amostras/%d dias ≠ %d/%d", ci, c.In.Metric,
				got.Samples, got.SpanDays, c.Out.Samples, c.Out.SpanDays)
		}
		if string(got.Confidence) != c.Out.Confidence {
			t.Errorf("caso %d (%s): confiança %q ≠ %q", ci, c.In.Metric, got.Confidence, c.Out.Confidence)
		}
	}
	t.Logf("%d casos", len(data.Cases))
}

func mesmoPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func str(v *float64) string {
	if v == nil {
		return "nil"
	}
	return jsonNum(*v)
}

func jsonNum(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
