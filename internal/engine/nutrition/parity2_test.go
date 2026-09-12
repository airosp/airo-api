package nutrition

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Paridade da substituição de alimentos e da adesão nutricional: 2652 casos
// produzidos pela implementação que corre hoje no cliente.

type tsFile2 struct {
	Subs []struct {
		FoodID string  `json:"foodId"`
		Style  string  `json:"style"`
		Budget string  `json:"budget"`
		Grams  float64 `json:"grams"`
		R      [][]any `json:"r"`
	} `json:"subs"`
	Avail []struct {
		Style  string   `json:"style"`
		Budget string   `json:"budget"`
		Cat    *string  `json:"cat"`
		Slot   *string  `json:"slot"`
		R      []string `json:"r"`
	} `json:"avail"`
	Adherence []struct {
		Days   int             `json:"days"`
		PerDay int             `json:"perDay"`
		Kcal   float64         `json:"kcal"`
		Prot   float64         `json:"prot"`
		Status string          `json:"status"`
		A      AdherenceResult `json:"a"`
	} `json:"adherence"`
}

func load2(t *testing.T) tsFile2 {
	t.Helper()
	raw, err := os.ReadFile("testdata/ts-cases2.json")
	if err != nil {
		t.Fatal(err)
	}
	var f tsFile2
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParityFoodSubstitutions(t *testing.T) {
	cases := load2(t).Subs
	for i, tc := range cases {
		food, ok := GetFood(tc.FoodID)
		if !ok {
			t.Fatalf("alimento %q não existe", tc.FoodID)
		}
		factor := tc.Grams / 100
		item := MealItem{
			FoodID: food.ID, Name: food.Name, Grams: tc.Grams, Kcal: food.Kcal * factor,
			Macros: MacrosFloat{
				Protein: food.Macros.Protein * factor,
				Carbs:   food.Macros.Carbs * factor,
				Fat:     food.Macros.Fat * factor,
			},
		}
		diet := DietProfile{
			Style: DietStyle(tc.Style), MealsPerDay: 4, Budget: Budget(tc.Budget),
			Exclusions: []string{"rice", "beans"},
		}
		got, err := FindSubstitutions(item, diet, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.R) {
			t.Errorf("caso %d (%s %s %s %.0fg): %d opções ≠ %d",
				i, tc.FoodID, tc.Style, tc.Budget, tc.Grams, len(got), len(tc.R))
			continue
		}
		for k, s := range got {
			w := tc.R[k]
			if s.Food.ID != w[0].(string) || s.Grams != int(w[1].(float64)) ||
				s.Kcal != int(w[2].(float64)) || s.Match != w[3].(float64) {
				t.Errorf("caso %d opção %d:\n Go {%s %dg %dkcal match=%v}\n TS %v",
					i, k, s.Food.ID, s.Grams, s.Kcal, s.Match, w)
			}
		}
	}
	t.Logf("%d conjuntos de substituições iguais ao TypeScript", len(cases))
}

func TestParityAvailableFoods(t *testing.T) {
	cases := load2(t).Avail
	for i, tc := range cases {
		in := AvailableFoodsInput{
			Style: DietStyle(tc.Style), Budget: Budget(tc.Budget), Exclusions: []string{"eggs"},
		}
		if tc.Cat != nil {
			in.Category = FoodCategory(*tc.Cat)
		}
		if tc.Slot != nil {
			in.Slot = Slot(*tc.Slot)
		}
		got, err := AvailableFoods(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.R) {
			t.Errorf("caso %d (%s %s cat=%v slot=%v): %d alimentos ≠ %d",
				i, tc.Style, tc.Budget, tc.Cat, tc.Slot, len(got), len(tc.R))
			continue
		}
		for k := range got {
			if got[k].ID != tc.R[k] {
				t.Errorf("caso %d posição %d: %q ≠ %q", i, k, got[k].ID, tc.R[k])
				break
			}
		}
	}
	t.Logf("%d listas de alimentos iguais ao TypeScript", len(cases))
}

func TestParityNutritionAdherence(t *testing.T) {
	cases := load2(t).Adherence
	strategy := Strategy{
		CalorieTarget: 2100,
		Macros:        Macros{Protein: 150, Carbs: 230, Fat: 60},
		MealsPerDay:   4,
	}
	anchor := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	for i, tc := range cases {
		var logs []Log
		for d := 0; d < tc.Days; d++ {
			for m := 0; m < tc.PerDay; m++ {
				at := anchor.AddDate(0, 0, d).Add(time.Duration(8+m*3) * time.Hour)
				portion := 1.0
				if m == 0 {
					portion = 0.5
				}
				logs = append(logs, Log{
					RecordedAtISO: at.Format("2006-01-02T15:04:05.000Z"),
					Status:        LogStatus(tc.Status),
					Kcal:          tc.Kcal,
					Macros:        MacrosFloat{Protein: tc.Prot},
					Portion:       portion,
				})
			}
		}
		got := ComputeAdherence(logs, strategy, "2026-09-01T00:00:00.000Z", "2026-10-01T00:00:00.000Z")

		eq := got.Calories == tc.A.Calories && got.Protein == tc.A.Protein &&
			got.Meals == tc.A.Meals && got.Score == tc.A.Score &&
			got.LoggedDays == tc.A.LoggedDays && got.Evaluable == tc.A.Evaluable &&
			eqIntPtr(got.AverageIntakeKcal, tc.A.AverageIntakeKcal) &&
			eqIntPtr(got.AverageProteinG, tc.A.AverageProteinG)
		if !eq {
			t.Errorf("caso %d (%dd × %d, %.0fkcal, %s):\n Go %+v\n TS %+v",
				i, tc.Days, tc.PerDay, tc.Kcal, tc.Status, got, tc.A)
		}
	}
	t.Logf("%d casos de adesão alimentar iguais ao TypeScript", len(cases))
}

func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
