package nutrition

import (
	"fmt"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// LoggedFood é um alimento do catálogo, na quantidade que alguém comeu.
type LoggedFood struct {
	FoodID string
	Grams  float64
}

// LoggedMeal é o valor de uma refeição registada, item a item e no total.
type LoggedMeal struct {
	Items  []MealItem
	Kcal   int
	Macros MacrosFloat
}

// MaxLoggedGrams é o tecto de um item num registo. Um quilo e meio de um só
// alimento numa refeição já não é uma refeição — é um engano no teclado.
const MaxLoggedGrams = 1500

/*
 * ValueLoggedMeal calcula o que uma refeição registada vale.
 *
 * É o único sítio de onde saem as calorias de um registo reconhecido por
 * fotografia ou por texto. O modelo diz **o quê** e, quando muito, **quanto**;
 * os números saem daqui, do catálogo, por 100 g — a mesma conta de `toItem`,
 * que é a que monta o plano. Duas contas para o mesmo alimento eram duas
 * respostas para a mesma pergunta.
 *
 * Os totais somam-se em bruto e arredondam-se uma vez no fim: somar parcelas
 * já arredondadas desviava o total do dia uma caloria por item.
 */
func ValueLoggedMeal(items []LoggedFood) (LoggedMeal, error) {
	if len(items) == 0 {
		return LoggedMeal{}, fmt.Errorf("refeição sem alimentos")
	}
	out := LoggedMeal{Items: make([]MealItem, 0, len(items))}
	var kcal, protein, carbs, fat float64
	for _, it := range items {
		food, ok := GetFood(it.FoodID)
		if !ok {
			return LoggedMeal{}, fmt.Errorf("alimento desconhecido: %q", it.FoodID)
		}
		if it.Grams <= 0 || it.Grams > MaxLoggedGrams {
			return LoggedMeal{}, fmt.Errorf("quantidade fora do intervalo para %q: %g g", it.FoodID, it.Grams)
		}
		out.Items = append(out.Items, toItem(food, it.Grams))
		factor := it.Grams / 100
		kcal += food.Kcal * factor
		protein += food.Macros.Protein * factor
		carbs += food.Macros.Carbs * factor
		fat += food.Macros.Fat * factor
	}
	out.Kcal = int(portable.RoundJS(kcal))
	out.Macros = MacrosFloat{
		Protein: portable.RoundTo(protein, 1),
		Carbs:   portable.RoundTo(carbs, 1),
		Fat:     portable.RoundTo(fat, 1),
	}
	return out, nil
}
