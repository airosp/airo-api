package nutrition

import (
	"encoding/json"
	"os"
	"testing"
)

// Paridade de `RebuildMeal`: 1 728 remontagens, 5 552 itens.
//
// Cobre os 4 estilos × 3 orçamentos × 3 contagens de refeições × 3 conjuntos de
// exclusões, e para cada refeição do dia as variantes 1, 2, 3 e 7 — a 7 está lá
// de propósito: é o número de vegetais, e foi com um passo de 7 que o vegetal
// deixou de mudar.
func TestParidadeRebuildMeal(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts-rebuild.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var data struct {
		Cases []struct {
			In struct {
				Meal tsMeal `json:"meal"`
				Diet struct {
					Style       DietStyle `json:"style"`
					MealsPerDay int       `json:"mealsPerDay"`
					Budget      Budget    `json:"budget"`
					Exclusions  []string  `json:"exclusions"`
				} `json:"diet"`
				Variant int `json:"variant"`
			} `json:"in"`
			Out []tsMealItem `json:"out"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}
	if len(data.Cases) == 0 {
		t.Fatal("sem casos")
	}

	cfg := DefaultConfig()
	itens := 0

	for ci, c := range data.Cases {
		items := make([]MealItem, 0, len(c.In.Meal.Items))
		for _, it := range c.In.Meal.Items {
			items = append(items, MealItem{
				FoodID: it.FoodID, Name: it.Name, Grams: it.Grams, Kcal: it.Kcal, Macros: it.Macros,
			})
		}
		got, err := RebuildMeal(cfg, RebuildMealInput{
			Meal: PlannedMeal{
				ID: c.In.Meal.ID, Slot: c.In.Meal.Slot, Title: c.In.Meal.Title,
				Role: c.In.Meal.Role, Kcal: c.In.Meal.Kcal, Macros: c.In.Meal.Macros,
				Items: items,
			},
			Diet: DietProfile{
				Style: c.In.Diet.Style, MealsPerDay: c.In.Diet.MealsPerDay,
				Budget: c.In.Diet.Budget, Exclusions: c.In.Diet.Exclusions,
			},
			Variant: c.In.Variant,
		})
		if err != nil {
			t.Fatalf("caso %d: %v", ci, err)
		}

		if len(got.Items) != len(c.Out) {
			t.Fatalf("caso %d (%s, variante %d): %d itens ≠ %d",
				ci, c.In.Meal.Slot, c.In.Variant, len(got.Items), len(c.Out))
		}
		for ii, want := range c.Out {
			have := got.Items[ii]
			itens++
			if have.FoodID != want.FoodID {
				t.Errorf("caso %d item %d: %q ≠ %q", ci, ii, have.FoodID, want.FoodID)
				continue
			}
			if have.Grams != want.Grams || have.Kcal != want.Kcal || have.Macros != want.Macros {
				t.Errorf("caso %d item %d (%s): %gg/%gkcal %+v ≠ %gg/%gkcal %+v",
					ci, ii, want.FoodID, have.Grams, have.Kcal, have.Macros,
					want.Grams, want.Kcal, want.Macros)
			}
		}
	}
	t.Logf("%d remontagens · %d itens", len(data.Cases), itens)
}

// INVARIANTE 6: remontar preserva o alvo calórico e o de macros da refeição.
// Trocar a refeição não pode desfazer as contas do dia.
func TestRemontarPreservaOsAlvos(t *testing.T) {
	cfg := DefaultConfig()
	diet := DietProfile{Style: Omnivore, MealsPerDay: 4, Budget: BudgetMedium}
	dia, err := BuildMeals(cfg, BuildMealsInput{
		DayISO: "2026-09-13", CalorieTarget: 2200,
		Macros: MacrosFloat{Protein: 165, Carbs: 247.5, Fat: 61.1},
		Diet:   diet,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, meal := range dia {
		for variante := 1; variante <= 5; variante++ {
			out, err := RebuildMeal(cfg, RebuildMealInput{Meal: meal, Diet: diet, Variant: variante})
			if err != nil {
				t.Fatal(err)
			}
			if out.Kcal != meal.Kcal {
				t.Errorf("%s variante %d: alvo passou de %d para %d kcal",
					meal.Slot, variante, meal.Kcal, out.Kcal)
			}
			if out.Macros != meal.Macros {
				t.Errorf("%s variante %d: macros mudaram %+v → %+v",
					meal.Slot, variante, meal.Macros, out.Macros)
			}
			if len(out.Items) == 0 {
				t.Errorf("%s variante %d: ficou vazia", meal.Slot, variante)
			}
		}
	}
}

// Paridade da **cadeia** de toques: 144 cadeias, 864 remontagens.
//
// Existe porque a bateria acima só remonta sobre a proposta original, e não é
// assim que se usa: cada toque parte do que está no ecrã, que já é o resultado
// do anterior. Um servidor que partisse sempre da proposta devolvia ao segundo
// toque o que o primeiro já tinha dado — a pessoa carregava e nada mudava.
func TestParidadeCadeiaDeTrocas(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts-chain.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var data struct {
		Cases []struct {
			In struct {
				DayISO        string      `json:"dayISO"`
				CalorieTarget int         `json:"calorieTarget"`
				Macros        MacrosFloat `json:"macros"`
				Diet          struct {
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
				Slot        Slot `json:"slot"`
			} `json:"in"`
			Cadeia []string `json:"cadeia"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}

	cfg := DefaultConfig()
	remontagens := 0

	for ci, c := range data.Cases {
		diet := DietProfile{
			Style: c.In.Diet.Style, MealsPerDay: c.In.Diet.MealsPerDay,
			Budget: c.In.Diet.Budget, Exclusions: c.In.Diet.Exclusions,
		}
		dia, err := BuildMeals(cfg, BuildMealsInput{
			DayISO: c.In.DayISO, CalorieTarget: c.In.CalorieTarget, Macros: c.In.Macros,
			Diet:        diet,
			Training:    TrainingLoad{SessionsPerWeek: c.In.Training.SessionsPerWeek, WorkoutTime: c.In.Training.WorkoutTime},
			TrainsToday: c.In.TrainsToday,
		})
		if err != nil {
			t.Fatalf("caso %d: %v", ci, err)
		}

		var meal PlannedMeal
		achou := false
		for _, m := range dia {
			if m.Slot == c.In.Slot {
				meal, achou = m, true
			}
		}
		if !achou {
			t.Fatalf("caso %d: sem refeição %s", ci, c.In.Slot)
		}

		for toque, quer := range c.Cadeia {
			meal, err = RebuildMeal(cfg, RebuildMealInput{Meal: meal, Diet: diet, Variant: toque + 1})
			if err != nil {
				t.Fatal(err)
			}
			remontagens++
			if got := assinatura(meal.Items); got != quer {
				t.Errorf("caso %d (%s) toque %d: %q ≠ %q", ci, c.In.Slot, toque+1, got, quer)
			}
			// O alvo não pode mudar — invariante 6, e é o que mantém o dia a fechar.
			if meal.Kcal != dia[0].Kcal && meal.Slot == dia[0].Slot {
				t.Errorf("caso %d: o alvo da refeição mudou com a troca", ci)
			}
		}
	}
	t.Logf("%d cadeias · %d remontagens", len(data.Cases), remontagens)
}
