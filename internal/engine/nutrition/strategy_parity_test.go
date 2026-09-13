package nutrition

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Paridade com o TypeScript: 900 estratégias e os 3 600 alvos de refeição que
// delas saem.
//
// Cobre os 5 objectivos × 4 estilos × 5 TDEE (incluindo **sem** TDEE, onde o
// recurso é `peso × 33`) × 3 gastos observados × 3 níveis de agressividade.
//
// Gerado a correr `buildStrategy` e `buildDayPlan` de
// `mobile/modules/nutrition-engine/nutrition-engine.ts`.
func TestParidadeEstrategiaEDia(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts-strategy.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var data struct {
		Cases []struct {
			In struct {
				GoalType         GoalType `json:"goalType"`
				BodyWeightKg     float64  `json:"bodyWeightKg"`
				TDEEKcal         *float64 `json:"tdeeKcal"`
				ObservedTDEEKcal *float64 `json:"observedTdeeKcal"`
				Aggressiveness   float64  `json:"aggressiveness"`
				Diet             struct {
					Style       DietStyle `json:"style"`
					MealsPerDay int       `json:"mealsPerDay"`
					Budget      Budget    `json:"budget"`
					Exclusions  []string  `json:"exclusions"`
				} `json:"diet"`
				Training struct {
					SessionsPerWeek int    `json:"sessionsPerWeek"`
					WorkoutTime     string `json:"workoutTime"`
				} `json:"training"`
				TrainsToday bool `json:"trainsToday"`
			} `json:"in"`
			Strategy struct {
				CalorieTarget    int    `json:"calorieTarget"`
				Macros           Macros `json:"macros"`
				EnergyAdjustment int    `json:"energyAdjustment"`
				BasisTDEE        int    `json:"basisTdee"`
				BasisSource      string `json:"basisSource"`
				MealsPerDay      int    `json:"mealsPerDay"`
			} `json:"strategy"`
			Day struct {
				Kcal       int      `json:"kcal"`
				MealKcal   []int    `json:"mealKcal"`
				FirstItems []string `json:"firstItems"`
			} `json:"day"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}
	if len(data.Cases) == 0 {
		t.Fatal("sem casos")
	}

	cfg := DefaultConfig()
	refeicoes := 0

	for ci, c := range data.Cases {
		diet := DietProfile{
			Style: c.In.Diet.Style, MealsPerDay: c.In.Diet.MealsPerDay,
			Budget: c.In.Diet.Budget, Exclusions: c.In.Diet.Exclusions,
		}
		got := BuildStrategy(cfg, BuildStrategyInput{
			Energy:           EnergyProfile{TDEEKcal: c.In.TDEEKcal},
			GoalType:         c.In.GoalType,
			BodyWeightKg:     c.In.BodyWeightKg,
			Diet:             diet,
			Aggressiveness:   c.In.Aggressiveness,
			ObservedTDEEKcal: c.In.ObservedTDEEKcal,
		})

		if got.CalorieTarget != c.Strategy.CalorieTarget {
			t.Errorf("caso %d (%s, %gkg): alvo %d ≠ %d", ci, c.In.GoalType, c.In.BodyWeightKg,
				got.CalorieTarget, c.Strategy.CalorieTarget)
		}
		if got.Macros != c.Strategy.Macros {
			t.Errorf("caso %d: macros %+v ≠ %+v", ci, got.Macros, c.Strategy.Macros)
		}
		if got.EnergyAdjustment != c.Strategy.EnergyAdjustment {
			t.Errorf("caso %d: ajuste %d ≠ %d", ci, got.EnergyAdjustment, c.Strategy.EnergyAdjustment)
		}
		if got.BasisTDEE != c.Strategy.BasisTDEE || got.BasisSource != c.Strategy.BasisSource {
			t.Errorf("caso %d: base %d/%s ≠ %d/%s", ci,
				got.BasisTDEE, got.BasisSource, c.Strategy.BasisTDEE, c.Strategy.BasisSource)
		}

		day, err := BuildDayPlan(cfg, BuildDayPlanInput{
			Strategy: got, Diet: diet,
			Training:    TrainingLoad{SessionsPerWeek: c.In.Training.SessionsPerWeek, WorkoutTime: c.In.Training.WorkoutTime},
			DayISO:      "2026-09-13",
			TrainsToday: c.In.TrainsToday,
		})
		if err != nil {
			t.Fatalf("caso %d: %v", ci, err)
		}

		if day.Kcal != c.Day.Kcal {
			t.Errorf("caso %d: dia %d ≠ %d kcal", ci, day.Kcal, c.Day.Kcal)
		}
		if len(day.Meals) != len(c.Day.MealKcal) {
			t.Fatalf("caso %d: %d refeições ≠ %d", ci, len(day.Meals), len(c.Day.MealKcal))
		}
		for mi, m := range day.Meals {
			refeicoes++
			if m.Kcal != c.Day.MealKcal[mi] {
				t.Errorf("caso %d refeição %d (%s): %d ≠ %d kcal", ci, mi, m.Slot, m.Kcal, c.Day.MealKcal[mi])
			}
			ids := make([]string, 0, len(m.Items))
			for _, it := range m.Items {
				ids = append(ids, it.FoodID)
			}
			if have := strings.Join(ids, "+"); have != c.Day.FirstItems[mi] {
				t.Errorf("caso %d refeição %d (%s): %q ≠ %q", ci, mi, m.Slot, have, c.Day.FirstItems[mi])
			}
		}
	}
	t.Logf("%d estratégias · %d refeições", len(data.Cases), refeicoes)
}

// INVARIANTE 4 à saída do plano do dia: a soma dos alvos das refeições é
// **exactamente** o alvo diário.
//
// A guarda em `BuildDayPlan` não se consegue disparar por aqui, e isso é de
// propósito: `SplitCalories` devolve a sobra do arredondamento à maior
// refeição, por isso a soma fecha sempre. A guarda espelha o `throw` do
// TypeScript e existe para o dia em que a repartição mudar — o que se testa é
// a propriedade, não a guarda.
func TestDiaFechaSempre(t *testing.T) {
	cfg := DefaultConfig()
	dias := 0

	for _, goal := range []GoalType{LoseFat, GainMuscle, Maintain, Performance, Health} {
		for _, style := range []DietStyle{Omnivore, HighProtein, Vegetarian, Vegan} {
			for _, refeicoes := range []int{3, 4, 5} {
				for _, peso := range []float64{48, 62, 78, 95, 120} {
					diet := DietProfile{Style: style, MealsPerDay: refeicoes, Budget: BudgetMedium}
					s := BuildStrategy(cfg, BuildStrategyInput{
						Energy: EnergyProfile{}, GoalType: goal, BodyWeightKg: peso, Diet: diet,
					})
					day, err := BuildDayPlan(cfg, BuildDayPlanInput{
						Strategy: s, Diet: diet, DayISO: "2026-09-13",
					})
					if err != nil {
						t.Fatalf("%s/%s/%d refeições/%gkg: %v", goal, style, refeicoes, peso, err)
					}
					dias++

					soma := 0
					for _, m := range day.Meals {
						soma += m.Kcal
					}
					if soma != day.Kcal {
						t.Errorf("%s/%s/%d/%gkg: refeições somam %d, o dia pede %d",
							goal, style, refeicoes, peso, soma, day.Kcal)
					}
					// INVARIANTE 2: o piso é absoluto, não um valor inicial.
					if day.Kcal < cfg.MinDailyCalories {
						t.Errorf("%s/%s/%gkg: %d kcal abaixo do piso %d",
							goal, style, peso, day.Kcal, cfg.MinDailyCalories)
					}
					// Uma refeição sem nada não é uma refeição.
					for _, m := range day.Meals {
						if len(m.Items) == 0 {
							t.Errorf("%s/%s/%gkg: %s ficou vazia", goal, style, peso, m.Slot)
						}
					}
				}
			}
		}
	}
	t.Logf("%d dias montados", dias)
}
