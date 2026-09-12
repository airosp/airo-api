package goal

import (
	"fmt"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// snap arredonda ao meio quilo. Uma sugestão de 69,73 kg não é uma sugestão.
func snap(v float64) float64 {
	return portable.RoundTo(portable.RoundJS(v*2)/2, 1)
}

var priorityWeight = map[string]int{"high": 0, "medium": 1, "low": 2}

// BuildRecommendations traduz sinais em saídas concretas.
//
// A Airo **propõe, nunca altera a meta sozinha** — por isso "Manter a minha
// meta" acompanha sempre qualquer sugestão de mudar o alvo. Sem essa opção, a
// lista de sugestões lê-se como uma correcção imposta.
func BuildRecommendations(c Config, signals []Signal, m Metrics) []Recommendation {
	wanted := map[string]bool{}
	for _, s := range signals {
		for _, id := range s.RecommendationIDs {
			wanted[id] = true
		}
	}

	current := m.CurrentWeightKg
	out := make([]Recommendation, 0, 4)
	add := func(r Recommendation) {
		for _, e := range out {
			if e.ID == r.ID {
				return
			}
		}
		out = append(out, r)
	}

	if wanted["correct_target"] {
		suggestion := current + 2
		if m.Direction == GainWeight {
			suggestion = current - 2
		}
		w := snap(suggestion)
		add(Recommendation{ID: "correct_target", Label: "Corrigir o peso", Priority: "high",
			Action: Action{Kind: ActionSetTarget, TargetWeightKg: &w}})
	}
	if wanted["switch_to_gain"] {
		d := GainWeight
		add(Recommendation{ID: "switch_to_gain", Label: "Mudar para ganhar massa", Priority: "high",
			Action: Action{Kind: ActionSetDirection, Direction: &d}})
	}
	if wanted["switch_to_loss"] {
		d := LoseWeight
		add(Recommendation{ID: "switch_to_loss", Label: "Mudar para perder gordura", Priority: "high",
			Action: Action{Kind: ActionSetDirection, Direction: &d}})
	}
	if wanted["first_milestone"] {
		step := c.Milestones.StepKg + 2
		first := current + step
		sign := "+"
		if m.Direction == LoseWeight {
			first = current - step
			sign = "−"
		}
		w := snap(first)
		add(Recommendation{
			ID:       "first_milestone",
			Label:    fmt.Sprintf("Começar por %s%g kg", sign, step),
			Priority: "medium",
			Action:   Action{Kind: ActionSetTarget, TargetWeightKg: &w},
		})
	}
	if wanted["add_training_day"] {
		add(Recommendation{ID: "add_training_day", Label: "Treinar mais um dia", Priority: "medium",
			Action: Action{Kind: ActionAddTrainingDay}})
	}
	if wanted["increase_duration"] {
		add(Recommendation{ID: "increase_duration", Label: "Alongar os treinos", Priority: "low",
			Action: Action{Kind: ActionIncreaseDuration}})
	}
	// Só faz sentido oferecer mais prazo a quem declarou um prazo.
	if wanted["extend_timeframe"] && m.Timeframe.Weeks != nil {
		add(Recommendation{ID: "extend_timeframe", Label: "Dar mais tempo ao objetivo", Priority: "high",
			Action: Action{Kind: ActionExtendTimeframe}})
	}
	if wanted["consult_nutritionist"] {
		add(Recommendation{ID: "consult_nutritionist", Label: "Falar com um nutricionista", Priority: "high",
			Action: Action{Kind: ActionConsultSpecialist, Role: "nutritionist"}})
	}

	touchesTarget := false
	for _, e := range out {
		if e.Action.Kind == ActionSetTarget {
			touchesTarget = true
			break
		}
	}
	if touchesTarget || wanted["keep_goal"] {
		add(Recommendation{ID: "keep_goal", Label: "Manter a minha meta", Priority: "low",
			Action: Action{Kind: ActionKeepGoal}})
	}

	// Estável: recomendações da mesma prioridade mantêm a ordem de inserção.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && priorityWeight[out[j].Priority] < priorityWeight[out[j-1].Priority]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
