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

// INVARIANTE 5 — a substituição preserva o estilo alimentar e as exclusões.
//
// É o invariante que dá sentido ao motor: propor queijo a quem é vegano, ou
// ovos a quem os excluiu, não é uma imprecisão — é a app a trair a única coisa
// que a pessoa lhe pediu para respeitar.
//
// A varredura é exaustiva: todos os alimentos × todos os estilos × todos os
// orçamentos × três porções.
func TestInvariant05_SubstitutionRespectsStyleAndExclusions(t *testing.T) {
	all, err := Foods()
	if err != nil {
		t.Fatal(err)
	}

	styles := []DietStyle{Omnivore, HighProtein, Vegetarian, Vegan}
	budgets := []Budget{BudgetLow, BudgetMedium, BudgetHigh}
	exclusions := []string{"rice", "beans", "eggs", "chicken"}

	checked, violations := 0, 0
	for _, food := range all {
		for _, style := range styles {
			for _, budget := range budgets {
				for _, grams := range []float64{80, 150, 250} {
					factor := grams / 100
					item := MealItem{
						FoodID: food.ID, Name: food.Name, Grams: grams, Kcal: food.Kcal * factor,
						Macros: MacrosFloat{
							Protein: food.Macros.Protein * factor,
							Carbs:   food.Macros.Carbs * factor,
							Fat:     food.Macros.Fat * factor,
						},
					}
					diet := DietProfile{Style: style, MealsPerDay: 4, Budget: budget, Exclusions: exclusions}

					options, err := FindSubstitutions(item, diet, 4)
					if err != nil {
						t.Fatal(err)
					}
					for _, o := range options {
						checked++

						if !hasStyle(o.Food, style) {
							violations++
							t.Errorf("%s → %s: %q não serve o estilo %q (serve %v)",
								food.ID, o.Food.ID, o.Food.Name, style, o.Food.Styles)
						}
						for _, ex := range exclusions {
							if o.Food.ID == ex {
								violations++
								t.Errorf("%s → %s: alimento excluído foi proposto", food.ID, o.Food.ID)
							}
						}
						if budgetRank[o.Food.Budget] > budgetRank[budget] {
							violations++
							t.Errorf("%s → %s: orçamento %q acima de %q",
								food.ID, o.Food.ID, o.Food.Budget, budget)
						}
						// Substituir dentro da mesma função na refeição: um
						// hidrato não substitui uma proteína.
						if o.Food.Category != food.Category {
							violations++
							t.Errorf("%s (%s) → %s (%s): categorias diferentes",
								food.ID, food.Category, o.Food.ID, o.Food.Category)
						}
						// A porção nunca sai do plausível.
						if float64(o.Grams) < o.Food.Serving*0.5-5 || float64(o.Grams) > o.Food.Serving*2+5 {
							violations++
							t.Errorf("%s → %s: %dg fora de [%.0f, %.0f]",
								food.ID, o.Food.ID, o.Grams, o.Food.Serving*0.5, o.Food.Serving*2)
						}
						// E nunca se propõe o próprio alimento.
						if o.Food.ID == food.ID {
							violations++
							t.Errorf("%s substituído por si próprio", food.ID)
						}
					}
				}
			}
		}
	}
	if violations > 0 {
		t.Fatalf("%d violações em %d opções verificadas", violations, checked)
	}
	t.Logf("%d opções de substituição verificadas, 0 violações", checked)
}

// Sem dias registados não se inventa uma percentagem.
func TestNutritionAdherenceNeedsData(t *testing.T) {
	a := ComputeAdherence(nil, Strategy{CalorieTarget: 2100, MealsPerDay: 4},
		"2026-09-01T00:00:00.000Z", "2026-10-01T00:00:00.000Z")
	if a.Evaluable || a.Score != 0 || a.AverageIntakeKcal != nil {
		t.Fatalf("sem registos: %+v", a)
	}
}

// Registar três dias em trinta não é 100% de adesão: a cobertura conta.
func TestCoverageCountsInAdherence(t *testing.T) {
	strategy := Strategy{CalorieTarget: 2100, Macros: Macros{Protein: 150}, MealsPerDay: 4}
	mk := func(days int) AdherenceResult {
		var logs []Log
		for d := 0; d < days; d++ {
			for m := 0; m < 4; m++ {
				logs = append(logs, Log{
					RecordedAtISO: time0ISO(d, 8+m*3), Status: LogEaten,
					Kcal: 525, Macros: MacrosFloat{Protein: 37.5}, Portion: 1,
				})
			}
		}
		return ComputeAdherence(logs, strategy, "2026-09-01T00:00:00.000Z", "2026-10-01T00:00:00.000Z")
	}
	poucos, muitos := mk(3), mk(28)
	if poucos.Calories != muitos.Calories {
		t.Fatal("as calorias médias são as mesmas nos dois casos")
	}
	if !(muitos.Score > poucos.Score) {
		t.Fatalf("28 dias registados pontuaram %v e 3 dias pontuaram %v", muitos.Score, poucos.Score)
	}
}

// Comer a mais é tão desvio como comer a menos.
func TestCalorieDeviationIsSymmetric(t *testing.T) {
	strategy := Strategy{CalorieTarget: 2000, Macros: Macros{Protein: 150}, MealsPerDay: 4}
	mk := func(perMeal float64) AdherenceResult {
		var logs []Log
		for d := 0; d < 20; d++ {
			for m := 0; m < 4; m++ {
				logs = append(logs, Log{
					RecordedAtISO: time0ISO(d, 8+m*3), Status: LogEaten,
					Kcal: perMeal, Macros: MacrosFloat{Protein: 37.5}, Portion: 1,
				})
			}
		}
		return ComputeAdherence(logs, strategy, "2026-09-01T00:00:00.000Z", "2026-10-01T00:00:00.000Z")
	}
	aMenos := mk(400) // 1600 kcal, −20%
	aMais := mk(600)  // 2400 kcal, +20%
	if aMenos.Calories != aMais.Calories {
		t.Fatalf("−20%% deu %v e +20%% deu %v — o desvio tem de ser simétrico",
			aMenos.Calories, aMais.Calories)
	}
}
