package nutrition

import (
	"math"
	"sort"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// MinMatch — abaixo disto a troca deixa de ser equivalente.
const MinMatch = 0.5

type MealItem struct {
	FoodID string      `json:"foodId"`
	Name   string      `json:"name"`
	Grams  float64     `json:"grams"`
	Kcal   float64     `json:"kcal"`
	Macros MacrosFloat `json:"macros"`
}

type DietProfile struct {
	Style       DietStyle
	MealsPerDay int
	Budget      Budget
	// Exclusions são ids a excluir por alergia, restrição ou simples aversão.
	Exclusions []string
}

type Substitution struct {
	Food  Food `json:"food"`
	Grams int  `json:"grams"`
	Kcal  int  `json:"kcal"`
	// Match — 0 a 1: quão perto fica do original em calorias e proteína.
	Match float64 `json:"match"`
}

// FindSubstitutions — trocar peixe por frango não pode ser aleatório.
//
// A alternativa tem de ser nutricionalmente compatível, e a **porção é
// recalculada** para isso. É por essa razão que a interface escreve "Porção
// equivalente: 170 g" e não apenas "170 g": esconder o recálculo faz o número
// parecer arbitrário.
func FindSubstitutions(item MealItem, diet DietProfile, limit int) ([]Substitution, error) {
	if limit <= 0 {
		limit = 4
	}
	original, ok := GetFood(item.FoodID)
	if !ok {
		return []Substitution{}, nil
	}

	candidates, err := AvailableFoods(AvailableFoodsInput{
		Style:      diet.Style,
		Budget:     diet.Budget,
		Exclusions: append(append([]string{}, diet.Exclusions...), original.ID),
		Category:   original.Category,
	})
	if err != nil {
		return nil, err
	}

	scored := make([]Substitution, 0, len(candidates))
	for _, f := range candidates {
		if s, ok := bestPortion(f, item); ok {
			scored = append(scored, s)
		}
	}
	// Estável: dois candidatos igualmente compatíveis mantêm a ordem do
	// catálogo, como o `sort` do JavaScript.
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Match > scored[j].Match })

	// Opções muito fracas são ruído: 400 g de feijão não substituem peixe seco.
	usable := make([]Substitution, 0, len(scored))
	for _, s := range scored {
		if s.Match >= MinMatch {
			usable = append(usable, s)
		}
	}
	out := usable
	if len(out) == 0 {
		// Mas devolver nada também não ajuda: mostram-se as duas melhores, e a
		// compatibilidade baixa aparece no número.
		out = scored
		if len(out) > 2 {
			out = out[:2]
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// bestPortion procura a porção que melhor equilibra calorias e proteína, dentro
// de quantidades plausíveis para aquele alimento.
//
// Igualar só a proteína produzia porções absurdas quando as densidades são
// muito diferentes — e depois apresentava-as como se fossem alternativas.
func bestPortion(food Food, item MealItem) (Substitution, bool) {
	// Entre metade e o dobro da porção habitual: é o que uma pessoa põe no
	// prato. Fora disso deixa de ser uma alternativa e passa a ser uma conta.
	min := food.Serving * 0.5
	max := food.Serving * 2
	const steps = 16

	var best Substitution
	found := false

	for i := 0; i <= steps; i++ {
		grams := min + (max-min)*float64(i)/steps
		factor := grams / 100
		kcal := food.Kcal * factor
		protein := food.Macros.Protein * factor

		kcalMatch := 1 - math.Min(1, math.Abs(kcal-item.Kcal)/math.Max(1, item.Kcal))
		proteinMatch := 1.0
		if item.Macros.Protein > 1 {
			proteinMatch = 1 - math.Min(1, math.Abs(protein-item.Macros.Protein)/math.Max(1, item.Macros.Protein))
		}
		match := portable.RoundTo(kcalMatch*0.5+proteinMatch*0.5, 2)

		if !found || match > best.Match {
			best = Substitution{
				Food: food,
				// Ao múltiplo de 5 g: ninguém pesa 137 g de arroz.
				Grams: int(portable.RoundJS(grams/5) * 5),
				Kcal:  int(portable.RoundJS(kcal)),
				Match: match,
			}
			found = true
		}
	}
	return best, found
}
