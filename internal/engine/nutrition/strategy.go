package nutrition

import (
	"errors"
	"fmt"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// Os dois passos que separam "quanto comer" de "o que comer".
//
// Porte de `buildStrategy` e `buildDayPlan` em
// `mobile/modules/nutrition-engine/nutrition-engine.ts`. São finos de propósito:
// o trabalho está nos calculadores, e aqui só se decide de onde vem o TDEE e
// se o dia fecha.

// EnergyProfile é o que a nutrição precisa de saber sobre o gasto. Vem do motor
// de objetivos, que é quem sabe de corpos.
type EnergyProfile struct {
	// TDEEKcal é nil quando falta altura ou idade para a fórmula.
	TDEEKcal *float64
}

type Strategy2 struct {
	GoalType      GoalType `json:"goalType"`
	CalorieTarget int      `json:"calorieTarget"`
	Macros        Macros   `json:"macros"`
	MealsPerDay   int      `json:"mealsPerDay"`
	// EnergyAdjustment é o ajuste face ao gasto — negativo em défice.
	EnergyAdjustment int `json:"energyAdjustment"`
	BasisTDEE        int `json:"basisTdee"`
	// BasisSource distingue o gasto que o corpo revelou do que a fórmula
	// previu. A interface precisa da diferença para poder explicar o número.
	BasisSource string `json:"basisSource"`
}

type BuildStrategyInput struct {
	Energy       EnergyProfile
	GoalType     GoalType
	BodyWeightKg float64
	Diet         DietProfile
	// Aggressiveness: 0 = ritmo confortável, 1 = o máximo que a configuração
	// permite.
	Aggressiveness float64
	// ObservedTDEEKcal, quando existe, manda sobre a estimativa: é o corpo a
	// corrigir a fórmula.
	ObservedTDEEKcal *float64
}

// BuildStrategy — quanto comer, e porquê esse número.
func BuildStrategy(c Config, in BuildStrategyInput) Strategy2 {
	// Sem altura ou idade não há TDEE; aí, e só aí, cai-se numa estimativa
	// grosseira. É pior do que a fórmula e melhor do que não dizer nada.
	basis := portable.RoundJS(in.BodyWeightKg * 33)
	source := "estimated"
	switch {
	case in.ObservedTDEEKcal != nil:
		basis = *in.ObservedTDEEKcal
		source = "observed"
	case in.Energy.TDEEKcal != nil:
		basis = *in.Energy.TDEEKcal
	}

	target := ComputeCalorieTarget(c, CalorieTargetInput{
		TDEEKcal: basis, GoalType: in.GoalType, Aggressiveness: in.Aggressiveness,
	})
	macros := DistributeMacros(c, target.Target, in.BodyWeightKg, in.GoalType)

	return Strategy2{
		GoalType:         in.GoalType,
		CalorieTarget:    target.Target,
		Macros:           macros,
		MealsPerDay:      in.Diet.MealsPerDay,
		EnergyAdjustment: target.Adjustment,
		BasisTDEE:        int(portable.RoundJS(basis)),
		BasisSource:      source,
	}
}

// ErrDiaNaoFecha — a soma dos alvos das refeições não dá o alvo do dia.
//
// INVARIANTE 4. É a que o defeito dos 90% violava: o cartão anunciava um total
// e as refeições somavam outro. Aqui é erro, não aviso — um dia que não fecha
// não se mostra a ninguém.
var ErrDiaNaoFecha = errors.New("as refeições não somam o alvo diário")

type DayPlan struct {
	DayISO string        `json:"dayISO"`
	Kcal   int           `json:"kcal"`
	Macros Macros        `json:"macros"`
	Meals  []PlannedMeal `json:"meals"`
}

type BuildDayPlanInput struct {
	Strategy    Strategy2
	Diet        DietProfile
	Training    TrainingLoad
	DayISO      string
	TrainsToday bool
}

// BuildDayPlan — o que comer hoje.
func BuildDayPlan(c Config, in BuildDayPlanInput) (DayPlan, error) {
	meals, err := BuildMeals(c, BuildMealsInput{
		DayISO:        in.DayISO,
		CalorieTarget: in.Strategy.CalorieTarget,
		Macros: MacrosFloat{
			Protein: float64(in.Strategy.Macros.Protein),
			Carbs:   float64(in.Strategy.Macros.Carbs),
			Fat:     float64(in.Strategy.Macros.Fat),
		},
		Diet:        in.Diet,
		Training:    in.Training,
		TrainsToday: in.TrainsToday,
	})
	if err != nil {
		return DayPlan{}, err
	}

	soma := 0
	for _, m := range meals {
		soma += m.Kcal
	}
	if soma != in.Strategy.CalorieTarget {
		return DayPlan{}, fmt.Errorf("%w: %d ≠ %d", ErrDiaNaoFecha, soma, in.Strategy.CalorieTarget)
	}

	return DayPlan{
		DayISO: in.DayISO,
		Kcal:   in.Strategy.CalorieTarget,
		Macros: in.Strategy.Macros,
		Meals:  meals,
	}, nil
}
