package journey

import (
	"fmt"
	"math"
	"strings"

	"github.com/airosp/airo-api/internal/engine/portable"
)

type ExecutedMinutes struct {
	// Recent é a semana mais recente; Baseline a média das anteriores.
	Recent   float64
	Baseline float64
}

type RiskInput struct {
	Adherence       Adherence
	Trend           *Trend
	BodyWeightKg    float64
	WeeksElapsed    int
	ExecutedMinutes *ExecutedMinutes
	// MovingTowardsTarget: nil quando não se sabe. `false` é diferente de "não
	// há dados" — e é a diferença que separa "não responde" de "ainda é cedo".
	MovingTowardsTarget *bool
	TargetReached       bool
	// NutritionAdherence distingue "não come bem" de "come bem e não responde".
	NutritionAdherence *float64
	NowISO             string
}

// DetectRisks procura sinais de problema nos factos, com a evidência que os
// sustenta.
//
// Cada risco carrega a prova. Sem ela não há como explicar à pessoa porque é
// que o plano vai mudar — e um plano que muda sem explicação é um plano em que
// não se confia.
func DetectRisks(c Config, in RiskInput) []Risk {
	risks := []Risk{}
	cfg := c.Risk
	id := func(t RiskType) string { return string(t) + "-" + in.NowISO }

	// Nada de julgar adesão antes de ter havido sessões para cumprir.
	if !in.Adherence.Evaluable {
		return risks
	}

	pct := func(v float64) int { return int(portable.RoundJS(v * 100)) }

	switch {
	case in.WeeksElapsed >= cfg.StalledStartWeeks && in.Adherence.CompletedSessions == 0:
		risks = append(risks, Risk{
			ID: id(StalledStart), Type: StalledStart, Level: LevelHigh, Confidence: High,
			Evidence:       []string{fmt.Sprintf("%d semanas sem nenhuma sessão registada.", in.WeeksElapsed)},
			Recommendation: "Voltar a começar com um plano mais leve do que o atual.",
		})
	case in.Adherence.Score < cfg.LowAdherence.High:
		risks = append(risks, Risk{
			ID: id(LowAdherence), Type: LowAdherence, Level: LevelHigh, Confidence: Medium,
			Evidence: []string{
				fmt.Sprintf("Adesão de %d%%.", pct(in.Adherence.Score)),
				fmt.Sprintf("%d de %d sessões concluídas.", in.Adherence.CompletedSessions, in.Adherence.PlannedSessions),
			},
			Recommendation: "Reduzir a exigência do plano até a rotina assentar.",
		})
	case in.Adherence.Score < cfg.LowAdherence.Medium:
		risks = append(risks, Risk{
			ID: id(LowAdherence), Type: LowAdherence, Level: LevelMedium, Confidence: Medium,
			Evidence:       []string{fmt.Sprintf("Adesão de %d%%.", pct(in.Adherence.Score))},
			Recommendation: "Simplificar o plano ou ajustar os dias de treino.",
		})
	}

	if in.Trend != nil && in.Trend.RatePerWeek != nil && in.BodyWeightKg > 0 {
		rate := *in.Trend.RatePerWeek
		rateRatio := math.Abs(rate) / in.BodyWeightKg

		if rateRatio > cfg.RapidChangeRatio && in.Trend.Confidence != Low {
			risks = append(risks, Risk{
				ID: id(RapidChange), Type: RapidChange, Level: LevelHigh, Confidence: in.Trend.Confidence,
				Evidence: []string{fmt.Sprintf("Variação de %s kg por semana.",
					strings.Replace(fmt.Sprintf("%.2f", rate), ".", ",", 1))},
				Recommendation: "Abrandar o ritmo e rever a alimentação com um profissional.",
			})
		}

		// Plateau só depois de tempo suficiente para significar alguma coisa.
		if math.Abs(rate) < cfg.PlateauRateKg &&
			in.Trend.SpanDays >= cfg.PlateauWeeks*7 &&
			in.Trend.Confidence != Low {
			risks = append(risks, Risk{
				ID: id(Plateau), Type: Plateau, Level: LevelMedium, Confidence: in.Trend.Confidence,
				Evidence: []string{fmt.Sprintf("Sem variação apreciável há %d semanas.",
					int(portable.RoundJS(float64(in.Trend.SpanDays)/7)))},
				Recommendation: "Mudar o estímulo de treino ou rever o plano alimentar.",
			})
		}
	}

	if in.ExecutedMinutes != nil && in.ExecutedMinutes.Baseline >= float64(cfg.VolumeSpikeFloorMinutes) {
		jump := (in.ExecutedMinutes.Recent - in.ExecutedMinutes.Baseline) / in.ExecutedMinutes.Baseline
		if jump > cfg.VolumeSpikeRatio {
			risks = append(risks, Risk{
				ID: id(VolumeSpike), Type: VolumeSpike, Level: LevelMedium, Confidence: High,
				Evidence:       []string{fmt.Sprintf("Volume semanal subiu %d%% de uma vez.", pct(jump))},
				Recommendation: "Subir a carga de forma mais gradual.",
			})
		}
	}

	// Cumprir tudo e mesmo assim afastar-se do alvo é um problema do plano, não
	// da pessoa — e a mensagem tem de dizer isso.
	movingAway := in.MovingTowardsTarget != nil && !*in.MovingTowardsTarget
	if !in.TargetReached && movingAway &&
		in.Adherence.Score >= c.Adaptation.StrongAdherence &&
		in.Trend != nil && in.Trend.Confidence != Low &&
		in.Trend.SpanDays >= cfg.PlateauWeeks*7 {

		evidence := []string{
			fmt.Sprintf("Adesão de %d%% ao plano.", pct(in.Adherence.Score)),
			"A tendência do peso afasta-se da meta há mais de três semanas.",
		}
		recommendation := "O treino está a ser cumprido: o que precisa de revisão é o lado alimentar."
		if in.NutritionAdherence != nil {
			evidence = append(evidence, fmt.Sprintf("Adesão alimentar de %d%%.", pct(*in.NutritionAdherence)))
			if *in.NutritionAdherence < 0.7 {
				recommendation = "O treino está a ser cumprido; a alimentação é que está a ficar para trás."
			} else {
				recommendation = "Treino e alimentação estão a ser cumpridos: o alvo calórico é que precisa de ser recalibrado."
			}
		}
		risks = append(risks, Risk{
			ID: id(NoResponse), Type: NoResponse, Level: LevelMedium, Confidence: in.Trend.Confidence,
			Evidence: evidence, Recommendation: recommendation,
		})
	}

	return risks
}
