package nutrition

import (
	"math"
	"testing"
	"testing/quick"
)

// Os invariantes de docs/backend/03-regras-de-negocio.md §5.
//
// Não são números: são propriedades. Cada um corresponde a um defeito que já
// aconteceu, e é por isso que correm num alvo próprio (`-run Invariant`).

// INVARIANTE 1 — as fracções de mealSplit somam 1,000 em cada linha.
// Um desvio reparte calorias que não existem.
func TestInvariant01_MealSplitSumsToOne(t *testing.T) {
	c := DefaultConfig()
	for n, split := range c.MealSplit {
		var sum float64
		for _, r := range split {
			sum += r
		}
		if math.Abs(sum-1) > 0.0005 {
			t.Errorf("mealSplit[%d] soma %.4f", n, sum)
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

// INVARIANTE 2 — calorie_target ≥ 1500 depois de QUALQUER ajuste.
// O piso é absoluto, não um valor inicial.
func TestInvariant02_CalorieFloorIsAbsolute(t *testing.T) {
	c := DefaultConfig()
	f := func(tdee uint16, goalPick uint8, aggr uint8) bool {
		goals := []GoalType{LoseFat, GainMuscle, Maintain, Performance, Health}
		out := ComputeCalorieTarget(c, CalorieTargetInput{
			TDEEKcal:       float64(tdee),
			GoalType:       goals[int(goalPick)%len(goals)],
			Aggressiveness: float64(aggr) / 255,
		})
		return out.Target >= c.MinDailyCalories
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 20000}); err != nil {
		t.Fatal(err)
	}

	// E o caso que o torna visível: um TDEE baixo com défice agressivo.
	out := ComputeCalorieTarget(c, CalorieTargetInput{TDEEKcal: 1600, GoalType: LoseFat, Aggressiveness: 1})
	if out.Target != 1500 {
		t.Fatalf("1600 kcal com −20%% devia ficar no piso, deu %d", out.Target)
	}
	if out.FlooredAt == nil {
		t.Fatal("o piso entrou em acção e a resposta não o diz — a interface não consegue explicar porquê")
	}
}

// INVARIANTE 3 — gordura ≥ 22% e hidratos ≥ 80 g depois de qualquer ajuste.
// Não podem ser a variável de ajuste.
func TestInvariant03_FatAndCarbFloors(t *testing.T) {
	c := DefaultConfig()
	goals := []GoalType{LoseFat, GainMuscle, Maintain, Performance, Health}
	for _, goal := range goals {
		for kcal := c.MinDailyCalories; kcal <= 4000; kcal += 37 {
			for _, kg := range []float64{45, 60, 71.8, 95, 130} {
				m := DistributeMacros(c, kcal, kg, goal)

				if m.Carbs < c.MinCarbsG {
					t.Fatalf("%s %d kcal %.1f kg: hidratos %d g < %d g",
						goal, kcal, kg, m.Carbs, c.MinCarbsG)
				}
				fatKcal := float64(m.Fat * kcalPerGramFat)
				minFat := float64(kcal) * c.MinFatRatio
				// Uma grama de folga: o piso é em kcal e a gordura é gravada em
				// gramas inteiras.
				if fatKcal < minFat-kcalPerGramFat {
					t.Fatalf("%s %d kcal %.1f kg: gordura %d g (%.0f kcal) < mínimo %.0f kcal",
						goal, kcal, kg, m.Fat, fatKcal, minFat)
				}
			}
		}
	}
}

// INVARIANTE 4 — a soma dos alvos das refeições é o alvo diário.
// Senão o dia não fecha.
func TestInvariant04_MealTargetsSumToDaily(t *testing.T) {
	c := DefaultConfig()
	for _, meals := range []int{3, 4, 5} {
		for kcal := 1200; kcal <= 4500; kcal++ {
			parts := SplitCalories(c, kcal, meals)
			sum := 0
			for _, p := range parts {
				sum += p
			}
			if sum != kcal {
				t.Fatalf("%d refeições, %d kcal: partes somam %d (%v)", meals, kcal, sum, parts)
			}
			if len(parts) != meals {
				t.Fatalf("%d refeições devolveu %d partes", meals, len(parts))
			}
		}
	}
}

// INVARIANTE 6 (parte nutricional) — deslocar macros preserva as calorias.
// Trocar a função de uma refeição não pode desfazer as contas do dia.
func TestInvariant06_MacroShiftPreservesCalories(t *testing.T) {
	c := DefaultConfig()
	base := Macros{Protein: 40, Carbs: 60, Fat: 18}
	for role := range c.RoleMacroShift {
		got := ShiftMacros(c, base, role)
		diff := got.Kcal() - base.Kcal()
		// Até 9 kcal de folga: uma grama de gordura. O arredondamento a gramas
		// inteiras não permite ser exacto, e mentir sobre isso seria pior.
		if diff < -kcalPerGramFat || diff > kcalPerGramFat {
			t.Errorf("%s: %d kcal → %d kcal (%+d)", role, base.Kcal(), got.Kcal(), diff)
		}
	}
	// Um deslocamento desconhecido não inventa nada.
	if got := ShiftMacros(c, base, "não-existe"); got != base {
		t.Errorf("função desconhecida devia devolver a base, deu %+v", got)
	}
}
