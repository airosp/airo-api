package nutrition

import (
	"encoding/json"
	"os"
	"testing"
)

// Paridade com o TypeScript.
//
// Os casos são produzidos pela implementação que corre hoje no cliente
// (`.engine-build/modules/nutrition-engine/`), não escritos à mão. É a única
// forma honesta de dizer que o porte preserva o comportamento: um teste escrito
// a partir da minha leitura do código testaria a minha leitura.
//
// Regenerar: ver docs/backend/04-arquitetura-go.md, secção de testes.

type tsCases struct {
	Calorie [][]any `json:"calorie"`
	Macros  [][]any `json:"macros"`
	Split   [][]any `json:"split"`
	TDEE    [][]any `json:"tdee"`
}

func loadTS(t *testing.T) tsCases {
	t.Helper()
	raw, err := os.ReadFile("testdata/ts-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c tsCases
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParityCalorieTarget(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).Calorie
	for _, tc := range cases {
		goal := GoalType(tc[0].(string))
		tdee := tc[1].(float64)
		aggr := tc[2].(float64)
		wantTarget := int(tc[3].(float64))
		wantAdj := int(tc[4].(float64))
		wantFloored := tc[5] != nil

		got := ComputeCalorieTarget(c, CalorieTargetInput{TDEEKcal: tdee, GoalType: goal, Aggressiveness: aggr})
		if got.Target != wantTarget || got.Adjustment != wantAdj || (got.FlooredAt != nil) != wantFloored {
			t.Errorf("%s tdee=%.0f a=%.2f: Go{%d,%d,piso=%v} ≠ TS{%d,%d,piso=%v}",
				goal, tdee, aggr, got.Target, got.Adjustment, got.FlooredAt != nil,
				wantTarget, wantAdj, wantFloored)
		}
	}
	t.Logf("%d casos de alvo calórico iguais ao TypeScript", len(cases))
}

func TestParityDistributeMacros(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).Macros
	for _, tc := range cases {
		goal := GoalType(tc[0].(string))
		kcal := int(tc[1].(float64))
		kg := tc[2].(float64)
		want := Macros{Protein: int(tc[3].(float64)), Carbs: int(tc[4].(float64)), Fat: int(tc[5].(float64))}

		if got := DistributeMacros(c, kcal, kg, goal); got != want {
			t.Errorf("%s %d kcal %.1f kg: Go%+v ≠ TS%+v", goal, kcal, kg, got, want)
		}
	}
	t.Logf("%d casos de macros iguais ao TypeScript", len(cases))
}

func TestParitySplitCalories(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).Split
	for _, tc := range cases {
		meals := int(tc[0].(float64))
		kcal := int(tc[1].(float64))
		rawWant := tc[2].([]any)

		got := SplitCalories(c, kcal, meals)
		if len(got) != len(rawWant) {
			t.Fatalf("%d refeições %d kcal: %d partes ≠ %d", meals, kcal, len(got), len(rawWant))
		}
		for i := range got {
			if got[i] != int(rawWant[i].(float64)) {
				t.Errorf("%d refeições %d kcal: Go%v ≠ TS%v", meals, kcal, got, rawWant)
				break
			}
		}
	}
	t.Logf("%d casos de repartição iguais ao TypeScript", len(cases))
}

func TestParityObservedTDEE(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).TDEE
	for _, tc := range cases {
		avg, dw, days := tc[0].(float64), tc[1].(float64), int(tc[2].(float64))
		got := ObservedTDEE(c, avg, dw, days)

		if tc[3] == nil {
			if got != nil {
				t.Errorf("avg=%.0f Δ=%.1f d=%d: Go deu %d, TS deu null", avg, dw, days, *got)
			}
			continue
		}
		want := int(tc[3].(float64))
		if got == nil || *got != want {
			t.Errorf("avg=%.0f Δ=%.1f d=%d: Go=%v ≠ TS=%d", avg, dw, days, got, want)
		}
	}
	t.Logf("%d casos de TDEE observado iguais ao TypeScript", len(cases))
}
