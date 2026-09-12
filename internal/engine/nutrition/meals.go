package nutrition

import "github.com/airosp/airo-api/internal/engine/portable"

// SplitCalories reparte o total diário pelas refeições.
//
// INVARIANTE 4: a soma das partes é **exactamente** o total. O resto do
// arredondamento é devolvido à maior refeição — sem isso, um dia de 2 000 kcal
// repartido por quatro dava 1 999 ou 2 001, e o erro reaparecia em cada ecrã
// que voltasse a somar.
func SplitCalories(c Config, totalKcal, mealsPerDay int) []int {
	split, ok := c.MealSplit[mealsPerDay]
	if !ok {
		split = c.MealSplit[4]
	}

	parts := make([]int, len(split))
	sum := 0
	for i, ratio := range split {
		parts[i] = int(portable.RoundJS(float64(totalKcal) * ratio))
		sum += parts[i]
	}

	if drift := totalKcal - sum; drift != 0 {
		largest := 0
		for i, v := range parts {
			if v > parts[largest] {
				largest = i
			}
		}
		parts[largest] += drift
	}
	return parts
}

func SlotsFor(c Config, mealsPerDay int) []Slot {
	slots, ok := c.SlotsByCount[mealsPerDay]
	if !ok {
		slots = c.SlotsByCount[4]
	}
	out := make([]Slot, len(slots))
	copy(out, slots)
	return out
}

// ShiftMacros desloca a composição de uma refeição mantendo as calorias.
//
// Antes do treino a energia vem dos hidratos; depois, a prioridade é repor e
// reparar. A gordura sai dos dois lados porque atrasa a digestão.
//
// As calorias mantêm-se porque o resultado é reescalado: sem isso, uma refeição
// pré-treino passava a ter mais calorias do que o plano pedia só por lhe terem
// deslocado os macros.
func ShiftMacros(c Config, base Macros, role string) Macros {
	shift, ok := c.RoleMacroShift[role]
	if !ok {
		return base
	}

	protein := float64(base.Protein) * shift.Protein
	carbs := float64(base.Carbs) * shift.Carbs
	fat := float64(base.Fat) * shift.Fat

	target := float64(base.Kcal())
	shifted := protein*kcalPerGramProtein + carbs*kcalPerGramCarbs + fat*kcalPerGramFat
	if shifted > 0 && target > 0 {
		k := target / shifted
		protein, carbs, fat = protein*k, carbs*k, fat*k
	}

	return Macros{
		Protein: int(portable.RoundJS(protein)),
		Carbs:   int(portable.RoundJS(carbs)),
		Fat:     int(portable.RoundJS(fat)),
	}
}
