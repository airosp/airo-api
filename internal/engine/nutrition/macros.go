package nutrition

import "github.com/airosp/airo-api/internal/engine/portable"

type Macros struct {
	Protein int `json:"protein"`
	Carbs   int `json:"carbs"`
	Fat     int `json:"fat"`
}

const (
	kcalPerGramProtein = 4
	kcalPerGramCarbs   = 4
	kcalPerGramFat     = 9
)

// DistributeMacros reparte o alvo calórico.
//
// A ordem de prioridade muda com o objetivo; o que não muda é que **nem a
// proteína nem a gordura são a variável de ajuste**. A proteína fixa-se
// primeiro a partir do peso corporal, a gordura tem um mínimo em fracção das
// calorias, e os hidratos absorvem o resto — com um piso próprio, abaixo do
// qual o treino colapsa.
func DistributeMacros(c Config, calorieTarget int, bodyWeightKg float64, goal GoalType) Macros {
	proteinPerKg := c.Protein[goal]
	protein := int(portable.RoundJS(bodyWeightKg * proteinPerKg))
	proteinKcal := float64(protein * kcalPerGramProtein)

	minFatKcal := float64(calorieTarget) * c.MinFatRatio
	minCarbsKcal := float64(c.MinCarbsG * kcalPerGramCarbs)

	remaining := float64(calorieTarget) - proteinKcal
	if remaining < 0 {
		remaining = 0
	}

	// Performance protege os hidratos antes da gordura; os restantes fazem o
	// inverso. Quem treina para render precisa do combustível disponível.
	var fatKcal, carbsKcal float64
	if goal == Performance {
		carbsKcal = max64(minCarbsKcal, remaining-minFatKcal)
		fatKcal = max64(minFatKcal, remaining-carbsKcal)
	} else {
		fatKcal = max64(minFatKcal, remaining*0.3)
		carbsKcal = max64(minCarbsKcal, remaining-fatKcal)
	}

	// O arredondamento não pode inventar calorias: o que sobra ou falta vai à
	// gordura, que é a única das três com folga acima do mínimo.
	if total := proteinKcal + fatKcal + carbsKcal; total > float64(calorieTarget) {
		fatKcal = max64(minFatKcal, fatKcal-(total-float64(calorieTarget)))
	}

	return Macros{
		Protein: protein,
		Carbs:   int(portable.RoundJS(carbsKcal / kcalPerGramCarbs)),
		Fat:     int(portable.RoundJS(fatKcal / kcalPerGramFat)),
	}
}

func (m Macros) Kcal() int {
	return int(portable.RoundJS(float64(
		m.Protein*kcalPerGramProtein + m.Carbs*kcalPerGramCarbs + m.Fat*kcalPerGramFat)))
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
