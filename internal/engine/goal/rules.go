package goal

import (
	"fmt"
	"strings"
)

// As regras são a parte do motor que **decide**. Cada uma olha para as métricas
// e devolve um sinal, ou nada.
//
// Nenhuma diz "não podes". Dizem o que está a limitar e o que se pode mexer —
// a Airo propõe, o utilizador decide.

type Rule struct {
	ID       string
	Category Category
	Severity Severity
	Evaluate func(c Config, in Input, m Metrics) *Signal
}

// kg formata com vírgula decimal: as mensagens vão directas para o ecrã.
func kg(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}

func percent(ratio float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", ratio*100), ".", ",", 1) + "%"
}

// Rules devolve todas as regras conhecidas, pela ordem em que o cliente as
// declara — `safety`, `body`, `rate`, `training`. A ordem importa porque a
// ordenação final por gravidade é estável.
func Rules() []Rule {
	return []Rule{
		// ── Segurança ────────────────────────────────────────────────────────
		{
			ID: "direction_conflict", Category: CatSafety, Severity: Warning,
			Evaluate: func(_ Config, in Input, m Metrics) *Signal {
				if in.Goal.Priority == nil {
					return nil
				}
				switch {
				case *in.Goal.Priority == PriorityMuscle && m.Direction == LoseWeight:
					return &Signal{
						ID: "direction_conflict", Category: CatSafety, Severity: Warning,
						Message:           "O teu objetivo é ganhar massa, mas marcaste um peso mais baixo do que o atual.",
						RecommendationIDs: []string{"switch_to_loss", "correct_target"},
					}
				case *in.Goal.Priority == PriorityWeight && m.Direction == GainWeight:
					return &Signal{
						ID: "direction_conflict", Category: CatSafety, Severity: Warning,
						Message:           "O teu objetivo é perder gordura, mas marcaste um peso mais alto do que o atual.",
						RecommendationIDs: []string{"switch_to_gain", "correct_target"},
					}
				}
				return nil
			},
		},
		{
			ID: "magnitude_large", Category: CatSafety, Severity: Critical,
			Evaluate: func(c Config, _ Input, m Metrics) *Signal {
				if m.Direction != LoseWeight || m.DeltaRatio < c.Magnitude.LargeRatio {
					return nil
				}
				return &Signal{
					ID: "magnitude_large", Category: CatSafety, Severity: Critical,
					Message: fmt.Sprintf(
						"Estás a pedir uma redução de %.0f%% do teu peso. Vale a pena fazer este caminho acompanhado.",
						m.DeltaRatio*100),
					RecommendationIDs: []string{"consult_nutritionist", "first_milestone", "keep_goal"},
				}
			},
		},
		{
			ID: "energy_floor_reached", Category: CatNutrition, Severity: Warning,
			Evaluate: func(c Config, _ Input, m Metrics) *Signal {
				// O extremo optimista do TDEE, para não alarmar por causa da
				// incerteza do BMR.
				if m.Energy.TdeeKcal == nil || m.Energy.DailyKcalEquivalent == nil || m.Direction != LoseWeight {
					return nil
				}
				tdee := float64(m.Energy.TdeeKcal.High)
				daily := float64(*m.Energy.DailyKcalEquivalent)
				floor := float64(c.Energy.MinDailyCalories)
				available := tdee - floor
				// Ficar um pouco abaixo do piso é normal em quem tem TDEE baixo;
				// só interessa avisar quando a meta pede muito mais do que o
				// piso comporta.
				if available > 0 && daily <= available*1.5 {
					return nil
				}
				return &Signal{
					ID: "energy_floor_reached", Category: CatNutrition, Severity: Warning,
					Message: fmt.Sprintf(
						"Este ritmo pediria menos de %d kcal por dia. A Airo não desce abaixo desse valor, por isso a perda vai ser mais lenta do que a meta pede.",
						c.Energy.MinDailyCalories),
					RecommendationIDs: []string{"extend_timeframe", "consult_nutritionist"},
				}
			},
		},

		// ── Corpo ────────────────────────────────────────────────────────────
		{
			ID: "target_below_reference", Category: CatBody, Severity: Critical,
			Evaluate: func(_ Config, _ Input, m Metrics) *Signal {
				if m.Body.Target == nil || *m.Body.Target != BelowReference {
					return nil
				}
				msg := "A meta fica abaixo do intervalo de referência para a tua altura."
				if r := m.Body.ReferenceRange; r != nil {
					msg = fmt.Sprintf(
						"A meta fica abaixo do intervalo de referência para a tua altura (%s–%s kg).",
						kg(r.MinKg), kg(r.MaxKg))
				}
				return &Signal{
					ID: "target_below_reference", Category: CatBody, Severity: Critical,
					Message:           msg,
					RecommendationIDs: []string{"consult_nutritionist", "keep_goal"},
				}
			},
		},
		{
			ID: "target_above_reference", Category: CatBody, Severity: Info,
			Evaluate: func(_ Config, _ Input, m Metrics) *Signal {
				if m.Direction != GainWeight || m.Body.Target == nil || *m.Body.Target != AboveReference {
					return nil
				}
				return &Signal{
					ID: "target_above_reference", Category: CatBody, Severity: Info,
					Message: "A meta fica acima do intervalo de referência de IMC — o que é comum em quem ganha massa muscular.",
				}
			},
		},
		{
			ID: "body_confidence_low", Category: CatBody, Severity: Info,
			Evaluate: func(_ Config, _ Input, m Metrics) *Signal {
				if m.Body.Confidence != Low {
					return nil
				}
				return &Signal{
					ID: "body_confidence_low", Category: CatBody, Severity: Info,
					Message: "Com a tua altura, a Airo consegue afinar esta avaliação.",
				}
			},
		},

		// ── Ritmo ────────────────────────────────────────────────────────────
		{
			ID: "rate_aggressive", Category: CatRate, Severity: Critical,
			Evaluate: func(c Config, _ Input, m Metrics) *Signal {
				ratio := m.Timeframe.RequiredWeeklyChangeRatio
				if ratio == nil || m.Direction == MaintainWeight {
					return nil
				}
				rates := c.WeeklyRate.Gain
				if m.Direction == LoseWeight {
					rates = c.WeeklyRate.Loss
				}
				if *ratio < rates.Aggressive {
					return nil
				}
				return &Signal{
					ID: "rate_aggressive", Category: CatRate, Severity: Critical,
					Message: fmt.Sprintf(
						"O prazo escolhido exige %s do teu peso por semana. É uma mudança muito rápida.", percent(*ratio)),
					RecommendationIDs: []string{"extend_timeframe", "consult_nutritionist"},
				}
			},
		},
		{
			ID: "rate_above_recommended", Category: CatRate, Severity: Warning,
			Evaluate: func(c Config, _ Input, m Metrics) *Signal {
				ratio := m.Timeframe.RequiredWeeklyChangeRatio
				if ratio == nil || m.Direction == MaintainWeight {
					return nil
				}
				rates := c.WeeklyRate.Gain
				if m.Direction == LoseWeight {
					rates = c.WeeklyRate.Loss
				}
				if *ratio < rates.RecommendedMax || *ratio >= rates.Aggressive {
					return nil
				}
				return &Signal{
					ID: "rate_above_recommended", Category: CatRate, Severity: Warning,
					Message: fmt.Sprintf(
						"O prazo pede %s do peso por semana, acima do ritmo habitualmente recomendado.", percent(*ratio)),
					RecommendationIDs: []string{"extend_timeframe"},
				}
			},
		},
		{
			ID: "magnitude_notable", Category: CatRate, Severity: Warning,
			Evaluate: func(c Config, _ Input, m Metrics) *Signal {
				if m.DeltaRatio < c.Magnitude.NotableRatio || m.DeltaRatio >= c.Magnitude.LargeRatio {
					return nil
				}
				suffix := ""
				if r := m.Timeframe.EstimatedWeeks; r != nil {
					suffix = fmt.Sprintf(" A um ritmo saudável, são cerca de %d a %d semanas.", r.Min, r.Max)
				}
				return &Signal{
					ID: "magnitude_notable", Category: CatRate, Severity: Warning,
					Message:           fmt.Sprintf("São %s do teu peso atual.%s", percent(m.DeltaRatio), suffix),
					RecommendationIDs: []string{"first_milestone"},
				}
			},
		},

		// ── Treino ───────────────────────────────────────────────────────────
		{
			ID: "training_insufficient", Category: CatTraining, Severity: Warning,
			Evaluate: func(_ Config, _ Input, m Metrics) *Signal {
				if m.Training.Adequacy != Insufficient {
					return nil
				}
				return &Signal{
					ID: "training_insufficient", Category: CatTraining, Severity: Warning,
					Message: fmt.Sprintf(
						"%d min de treino por semana é pouco tempo para uma meta desta dimensão.", m.Training.WeeklyMinutes),
					RecommendationIDs: []string{"add_training_day", "increase_duration", "extend_timeframe"},
				}
			},
		},
		{
			ID: "training_limited", Category: CatTraining, Severity: Info,
			Evaluate: func(_ Config, _ Input, m Metrics) *Signal {
				if m.Training.Adequacy != Limited {
					return nil
				}
				return &Signal{
					ID: "training_limited", Category: CatTraining, Severity: Info,
					Message: fmt.Sprintf(
						"Com %d min por semana, a meta é possível mas vai pedir mais tempo.", m.Training.WeeklyMinutes),
					RecommendationIDs: []string{"add_training_day", "increase_duration"},
				}
			},
		},
	}
}

var severityOrder = map[Severity]int{Critical: 0, Warning: 1, Info: 2}

// EvaluateRules corre todas as regras e devolve os sinais, do mais grave para
// o menos.
//
// A ordenação é **estável**: regras da mesma gravidade mantêm a ordem em que
// foram declaradas. Sem isso, dois servidores dariam a mesma avaliação por
// ordens diferentes, e a primeira mensagem — a que a pessoa lê — mudava.
func EvaluateRules(c Config, in Input, m Metrics) []Signal {
	out := make([]Signal, 0, 4)
	for _, rule := range Rules() {
		if s := rule.Evaluate(c, in, m); s != nil {
			out = append(out, *s)
		}
	}
	stableSortSignals(out)
	return out
}

func stableSortSignals(s []Signal) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && severityOrder[s[j].Severity] < severityOrder[s[j-1].Severity]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
