package service

import (
	"testing"

	"github.com/airosp/airo-api/internal/engine/nutrition"
)

// As quatro combinações que a app envia, e o objectivo nutricional de cada uma.
//
// É a inversão de `PARA_SERVIDOR` em `mobile/lib/api/goal-map.ts`. Esteve presa
// em `maintain`: quem pedia para ganhar massa recebia um alvo de manutenção, e
// ninguém reparou porque nada lia a estratégia de volta.
func TestObjetivoNutricionalSaiDoObjetivoDeclarado(t *testing.T) {
	casos := []struct {
		nome                      string
		tipo, direcao, prioridade string
		quer                      nutrition.GoalType
	}{
		{"perder gordura", "outcome", "lose_weight", "weight", nutrition.LoseFat},
		{"ganhar massa", "outcome", "gain_weight", "muscle", nutrition.GainMuscle},
		{"ganhar força", "performance", "maintain_weight", "performance", nutrition.Performance},
		{"criar hábito", "behavior", "maintain_weight", "health", nutrition.Health},
		// Do contrato, não da app.
		{"manutenção declarada", "maintenance", "maintain_weight", "weight", nutrition.Maintain},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := nutritionGoalOf(CreateGoalInput{
				Type: c.tipo, Direction: c.direcao, Priority: c.prioridade,
				// O valor por omissão do perfil não pode ganhar ao objectivo
				// que a pessoa declarou — era exactamente esse o defeito.
				NutritionGoal: "maintain",
			})
			if got != c.quer {
				t.Errorf("%s: deu %q, esperava %q", c.nome, got, c.quer)
			}
		})
	}
}

// Ganhar massa tem de dar um alvo **acima** do gasto, e perder gordura abaixo.
// É a prova de que o mapa chega ao sítio onde conta.
func TestObjetivoMudaOAlvoCalorico(t *testing.T) {
	cfg := nutrition.DefaultConfig()
	alvo := func(g nutrition.GoalType) int {
		return nutrition.ComputeCalorieTarget(cfg, nutrition.CalorieTargetInput{
			TDEEKcal: 2334, GoalType: g,
		}).Target
	}
	manter, ganhar, perder := alvo(nutrition.Maintain), alvo(nutrition.GainMuscle), alvo(nutrition.LoseFat)

	if ganhar <= manter {
		t.Errorf("ganhar massa: %d kcal, manutenção %d — devia ser mais", ganhar, manter)
	}
	if perder >= manter {
		t.Errorf("perder gordura: %d kcal, manutenção %d — devia ser menos", perder, manter)
	}
	t.Logf("perder %d · manter %d · ganhar %d kcal", perder, manter, ganhar)
}
