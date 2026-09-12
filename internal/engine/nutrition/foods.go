package nutrition

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// A base de alimentos, por 100 g.
//
// Inclui de propósito o que se come em Moçambique — um plano que sugere salmão
// e abacate a quem cozinha xima e feijão não é um plano, é um folheto. Mas a
// biblioteca é global: a região **pondera**, não filtra.
//
// Vive em JSON e não em código: é catálogo, muda com muito mais frequência do
// que o motor, e o mesmo ficheiro serve de seed da tabela `food`.
//
//go:embed data/foods.json
var foodsJSON []byte

type FoodCategory string

const (
	Protein   FoodCategory = "protein"
	Carb      FoodCategory = "carb"
	Vegetable FoodCategory = "vegetable"
	Fat       FoodCategory = "fat"
	Fruit     FoodCategory = "fruit"
	Dairy     FoodCategory = "dairy"
)

type Budget string

const (
	BudgetLow    Budget = "low"
	BudgetMedium Budget = "medium"
	BudgetHigh   Budget = "high"
)

type DietStyle string

const (
	Omnivore    DietStyle = "omnivore"
	HighProtein DietStyle = "high_protein"
	Vegetarian  DietStyle = "vegetarian"
	Vegan       DietStyle = "vegan"
)

type Food struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Category FoodCategory `json:"category"`
	// Por 100 g, sempre. Normalizar aqui evita converter em vinte sítios.
	Kcal   float64     `json:"kcal"`
	Macros MacrosFloat `json:"macros"`
	Budget Budget      `json:"budget"`
	Styles []DietStyle `json:"styles"`
	// Serving é a porção habitual, em gramas.
	Serving float64 `json:"serving"`
	GoodFor []Slot  `json:"goodFor,omitempty"`
	Region  string  `json:"region,omitempty"`
}

// MacrosFloat é o que está no catálogo: por 100 g, com casas decimais.
// Distinto de `Macros`, que é o alvo de um dia em gramas inteiras.
type MacrosFloat struct {
	Protein float64 `json:"protein"`
	Carbs   float64 `json:"carbs"`
	Fat     float64 `json:"fat"`
}

var (
	foodsOnce sync.Once
	foods     []Food
	foodsErr  error
	foodByID  map[string]Food
)

func loadFoods() {
	foodsOnce.Do(func() {
		if err := json.Unmarshal(foodsJSON, &foods); err != nil {
			foodsErr = fmt.Errorf("base de alimentos ilegível: %w", err)
			return
		}
		foodByID = make(map[string]Food, len(foods))
		for _, f := range foods {
			if _, dup := foodByID[f.ID]; dup {
				foodsErr = fmt.Errorf("alimento repetido: %q", f.ID)
				return
			}
			foodByID[f.ID] = f
		}
	})
}

func Foods() ([]Food, error) {
	loadFoods()
	if foodsErr != nil {
		return nil, foodsErr
	}
	out := make([]Food, len(foods))
	copy(out, foods)
	return out, nil
}

func GetFood(id string) (Food, bool) {
	loadFoods()
	if foodsErr != nil {
		return Food{}, false
	}
	f, ok := foodByID[id]
	return f, ok
}

var budgetRank = map[Budget]int{BudgetLow: 0, BudgetMedium: 1, BudgetHigh: 2}

type AvailableFoodsInput struct {
	Style      DietStyle
	Budget     Budget
	Exclusions []string
	Category   FoodCategory
	Slot       Slot
}

// AvailableFoods — alimentos compatíveis com o estilo, o orçamento e as
// exclusões declaradas.
func AvailableFoods(in AvailableFoodsInput) ([]Food, error) {
	all, err := Foods()
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(in.Exclusions))
	for _, id := range in.Exclusions {
		excluded[id] = true
	}

	match := func(withSlot bool) []Food {
		out := []Food{}
		for _, f := range all {
			if !hasStyle(f, in.Style) || budgetRank[f.Budget] > budgetRank[in.Budget] || excluded[f.ID] {
				continue
			}
			if in.Category != "" && f.Category != in.Category {
				continue
			}
			if withSlot && in.Slot != "" && len(f.GoodFor) > 0 && !hasSlot(f, in.Slot) {
				continue
			}
			out = append(out, f)
		}
		return out
	}

	matches := match(true)
	// Um filtro de refeição demasiado apertado não pode deixar a lista vazia.
	if len(matches) > 0 || in.Slot == "" {
		return matches, nil
	}
	return match(false), nil
}

func hasStyle(f Food, s DietStyle) bool {
	for _, x := range f.Styles {
		if x == s {
			return true
		}
	}
	return false
}

func hasSlot(f Food, s Slot) bool {
	for _, x := range f.GoodFor {
		if x == s {
			return true
		}
	}
	return false
}
