package nutrition

import (
	"encoding/json"
	"os"
	"testing"
)

// Paridade com o TypeScript: 288 dias montados, 1 152 refeições, 3 951 itens.
//
// Cobre os 4 estilos alimentares × 3 orçamentos × 3 contagens de refeições × 4
// horários de treino (incluindo sem treino) × treina/não treina, com alvos
// calóricos, dias e exclusões a rodar por cima.
//
// Gerado a correr `buildMeals` de
// `mobile/modules/nutrition-engine/engines/meal-engine.ts`.

type tsMealItem struct {
	FoodID string      `json:"foodId"`
	Name   string      `json:"name"`
	Grams  float64     `json:"grams"`
	Kcal   float64     `json:"kcal"`
	Macros MacrosFloat `json:"macros"`
}

type tsMeal struct {
	ID     string       `json:"id"`
	Slot   Slot         `json:"slot"`
	Title  string       `json:"title"`
	Role   string       `json:"role"`
	Kcal   int          `json:"kcal"`
	Macros MacrosFloat  `json:"macros"`
	Items  []tsMealItem `json:"items"`
}

type tsMealCase struct {
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
	} `json:"in"`
	Out []tsMeal `json:"out"`
}

func TestParidadeBuildMeals(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts-meals.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var data struct {
		Cases []tsMealCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}
	if len(data.Cases) == 0 {
		t.Fatal("sem casos")
	}

	cfg := DefaultConfig()
	refeicoes, itens := 0, 0

	for ci, c := range data.Cases {
		got, err := BuildMeals(cfg, BuildMealsInput{
			DayISO:        c.In.DayISO,
			CalorieTarget: c.In.CalorieTarget,
			Macros:        c.In.Macros,
			Diet: DietProfile{
				Style: c.In.Diet.Style, MealsPerDay: c.In.Diet.MealsPerDay,
				Budget: c.In.Diet.Budget, Exclusions: c.In.Diet.Exclusions,
			},
			Training: TrainingLoad{
				SessionsPerWeek: c.In.Training.SessionsPerWeek,
				WorkoutTime:     c.In.Training.WorkoutTime,
			},
			TrainsToday: c.In.TrainsToday,
		})
		if err != nil {
			t.Fatalf("caso %d: %v", ci, err)
		}

		if len(got) != len(c.Out) {
			t.Fatalf("caso %d (%s, %s, %d refeições): Go deu %d refeições, TS %d",
				ci, c.In.Diet.Style, c.In.Diet.Budget, c.In.Diet.MealsPerDay, len(got), len(c.Out))
		}

		for mi, want := range c.Out {
			have := got[mi]
			refeicoes++
			onde := func(campo string) string {
				return "caso " + itoa(ci) + " refeição " + itoa(mi) + " (" + string(want.Slot) + ") " + campo
			}
			if have.ID != want.ID {
				t.Errorf("%s: %q ≠ %q", onde("id"), have.ID, want.ID)
			}
			if have.Title != want.Title {
				t.Errorf("%s: %q ≠ %q", onde("título"), have.Title, want.Title)
			}
			if have.Role != want.Role {
				t.Errorf("%s: %q ≠ %q", onde("papel"), have.Role, want.Role)
			}
			if have.Kcal != want.Kcal {
				t.Errorf("%s: %d ≠ %d", onde("kcal"), have.Kcal, want.Kcal)
			}
			if have.Macros != want.Macros {
				t.Errorf("%s: %+v ≠ %+v", onde("macros"), have.Macros, want.Macros)
			}
			if len(have.Items) != len(want.Items) {
				t.Errorf("%s: %d ≠ %d itens", onde("nº de itens"), len(have.Items), len(want.Items))
				continue
			}
			for ii, wantItem := range want.Items {
				haveItem := have.Items[ii]
				itens++
				if haveItem.FoodID != wantItem.FoodID {
					t.Errorf("%s item %d: alimento %q ≠ %q", onde(""), ii, haveItem.FoodID, wantItem.FoodID)
					continue
				}
				if haveItem.Grams != wantItem.Grams || haveItem.Kcal != wantItem.Kcal {
					t.Errorf("%s item %d (%s): %gg/%gkcal ≠ %gg/%gkcal", onde(""), ii, wantItem.FoodID,
						haveItem.Grams, haveItem.Kcal, wantItem.Grams, wantItem.Kcal)
				}
				if haveItem.Macros != wantItem.Macros {
					t.Errorf("%s item %d (%s): macros %+v ≠ %+v", onde(""), ii, wantItem.FoodID,
						haveItem.Macros, wantItem.Macros)
				}
			}
		}
	}
	t.Logf("%d casos · %d refeições · %d itens", len(data.Cases), refeicoes, itens)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
