package journey

import (
	"math"
	"sort"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// A tendência de uma métrica ao longo do tempo.
//
// Porte de `computeTrend` em
// `mobile/modules/journey-engine/engines/progress-engine.ts`.
//
// **Média móvel, não a última leitura.** Pesar-se duas vezes no mesmo dia não é
// uma tendência, e uma leitura isolada num corpo que retém água de um dia para
// o outro diz mais sobre o sal do jantar do que sobre gordura.

// Measurement é uma leitura de uma métrica num instante.
type Measurement struct {
	Metric     string
	Value      float64
	RecordedAt time.Time
}

// MetricasRuidosas — as que variam por razões que não são progresso.
//
// Uma métrica ruidosa com poucas amostras **nunca** passa de confiança baixa,
// por mais bem comportadas que as leituras pareçam.
var MetricasRuidosas = map[string]bool{
	"body_weight": true,
	"body_fat":    true,
	"waist":       true,
}

// ComputeTrend devolve nil quando não há base para decidir.
//
// Nil e não um zero: um número devolvido a partir de uma leitura parece
// informação, e uma interface que o mostre está a mentir com precisão.
func ComputeTrend(c Config, metric string, measurements []Measurement, now time.Time) *Trend {
	relevant := make([]Measurement, 0, len(measurements))
	for _, m := range measurements {
		if m.Metric == metric {
			relevant = append(relevant, m)
		}
	}
	if len(relevant) < c.Trend.MinSamples {
		return nil
	}
	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].RecordedAt.Before(relevant[j].RecordedAt)
	})

	window := time.Duration(c.Trend.WindowDays) * 24 * time.Hour
	naJanela := func(from, to time.Time) []float64 {
		out := []float64{}
		for _, m := range relevant {
			if m.RecordedAt.After(from) && !m.RecordedAt.After(to) {
				out = append(out, m.Value)
			}
		}
		return out
	}

	// Janela recente e janela anterior. Quando a recente fica vazia — registos
	// espaçados — cai-se para as duas últimas leituras: é pouco, mas é o que há,
	// e a confiança dirá que é pouco.
	recentes := naJanela(now.Add(-window), now)
	anteriores := naJanela(now.Add(-2*window), now.Add(-window))
	if len(recentes) == 0 {
		n := len(relevant)
		de := n - 2
		if de < 0 {
			de = 0
		}
		for _, m := range relevant[de:] {
			recentes = append(recentes, m.Value)
		}
	}

	rolling := portable.RoundTo(media(recentes), 2)
	var previous *float64
	if len(anteriores) > 0 {
		v := portable.RoundTo(media(anteriores), 2)
		previous = &v
	}

	first, last := relevant[0], relevant[len(relevant)-1]
	spanDays := int(portable.RoundJS(last.RecordedAt.Sub(first.RecordedAt).Hours() / 24))
	if spanDays < 1 {
		spanDays = 1
	}

	var rate *float64
	switch {
	case previous != nil:
		v := portable.RoundTo((rolling-*previous)/float64(c.Trend.WindowDays)*7, 3)
		rate = &v
	case spanDays >= 7:
		v := portable.RoundTo((last.Value-first.Value)/float64(spanDays)*7, 3)
		rate = &v
	}

	confidence := Low
	switch {
	case len(relevant) >= 6 && spanDays >= 14:
		confidence = High
	case len(relevant) >= 3 && spanDays >= 7:
		confidence = Medium
	}
	if MetricasRuidosas[metric] && len(relevant) < 3 {
		confidence = Low
	}

	return &Trend{
		RollingAverage:  rolling,
		PreviousAverage: previous,
		RatePerWeek:     rate,
		Samples:         len(relevant),
		SpanDays:        spanDays,
		Confidence:      confidence,
	}
}

func media(vs []float64) float64 {
	if len(vs) == 0 {
		return math.NaN()
	}
	total := 0.0
	for _, v := range vs {
		total += v
	}
	return total / float64(len(vs))
}
