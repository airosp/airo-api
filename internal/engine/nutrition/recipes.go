package nutrition

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/airosp/airo-api/internal/engine/portable"
)

//go:embed data/recipes.json
var recipesJSON []byte

// Receitas — combinações que as pessoas realmente cozinham, em vez de uma lista
// de ingredientes soltos.
//
// O valor nutricional **nunca** está escrito aqui: calcula-se a partir dos
// alimentos. Se um alimento mudar, a receita acompanha; escrito à mão, ficava
// uma segunda verdade a divergir em silêncio.

type RecipeIngredient struct {
	FoodID string  `json:"foodId"`
	Grams  float64 `json:"grams"`
}

type Recipe struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Slots       []Slot             `json:"slots"`
	Styles      []DietStyle        `json:"styles"`
	Ingredients []RecipeIngredient `json:"ingredients"`
	// Serves são as porções que a receita rende.
	Serves int `json:"serves"`
}

type RecipeNutrition struct {
	Kcal   float64     `json:"kcal"`
	Macros MacrosFloat `json:"macros"`
}

var (
	recipesOnce sync.Once
	recipes     []Recipe
	recipesErr  error
)

func loadRecipes() {
	recipesOnce.Do(func() {
		if err := json.Unmarshal(recipesJSON, &recipes); err != nil {
			recipesErr = fmt.Errorf("base de receitas ilegível: %w", err)
			return
		}
		visto := make(map[string]bool, len(recipes))
		for _, r := range recipes {
			if visto[r.ID] {
				recipesErr = fmt.Errorf("receita repetida: %q", r.ID)
				return
			}
			visto[r.ID] = true
		}
	})
}

var receitasGravadas atomic.Pointer[[]Recipe]

// ReplaceRecipes troca as receitas pelas gravadas. Uma lista vazia é válida —
// o motor monta refeições com alimentos soltos —, mas repetidas não.
func ReplaceRecipes(list []Recipe) error {
	visto := make(map[string]bool, len(list))
	for _, r := range list {
		if r.ID == "" {
			return fmt.Errorf("receita sem id na base gravada")
		}
		if visto[r.ID] {
			return fmt.Errorf("receita repetida na base gravada: %q", r.ID)
		}
		visto[r.ID] = true
	}
	copia := make([]Recipe, len(list))
	copy(copia, list)
	receitasGravadas.Store(&copia)
	return nil
}

// ResetRecipes volta ao JSON embutido. Só os testes o usam.
func ResetRecipes() { receitasGravadas.Store(nil) }

// EmbeddedRecipes é o JSON embutido — o que o seed grava.
func EmbeddedRecipes() ([]Recipe, error) {
	loadRecipes()
	if recipesErr != nil {
		return nil, recipesErr
	}
	out := make([]Recipe, len(recipes))
	copy(out, recipes)
	return out, nil
}

func Recipes() ([]Recipe, error) {
	if g := receitasGravadas.Load(); g != nil {
		out := make([]Recipe, len(*g))
		copy(out, *g)
		return out, nil
	}
	loadRecipes()
	if recipesErr != nil {
		return nil, recipesErr
	}
	out := make([]Recipe, len(recipes))
	copy(out, recipes)
	return out, nil
}

// ComputeRecipeNutrition soma os ingredientes e divide pelas porções.
func ComputeRecipeNutrition(r Recipe) RecipeNutrition {
	var kcal, protein, carbs, fat float64
	for _, ing := range r.Ingredients {
		food, ok := GetFood(ing.FoodID)
		if !ok {
			continue
		}
		factor := ing.Grams / 100
		kcal += food.Kcal * factor
		protein += food.Macros.Protein * factor
		carbs += food.Macros.Carbs * factor
		fat += food.Macros.Fat * factor
	}

	serves := float64(r.Serves)
	if serves < 1 {
		serves = 1
	}
	return RecipeNutrition{
		Kcal: portable.RoundJS(kcal / serves),
		Macros: MacrosFloat{
			Protein: portable.RoundTo(protein/serves, 1),
			Carbs:   portable.RoundTo(carbs/serves, 1),
			Fat:     portable.RoundTo(fat/serves, 1),
		},
	}
}

type AvailableRecipesInput struct {
	Style      DietStyle
	Slot       Slot
	Exclusions []string
}

// AvailableRecipes — as que servem o estilo e a refeição, sem nada excluído.
//
// A ordem é a do ficheiro, e tem de ser: a escolha do dia é `seed % n`, e
// reordenar aqui mudava a ementa de toda a gente.
func AvailableRecipes(in AvailableRecipesInput) ([]Recipe, error) {
	all, err := Recipes()
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(in.Exclusions))
	for _, id := range in.Exclusions {
		excluded[id] = true
	}

	out := []Recipe{}
	for _, r := range all {
		if !temEstilo(r.Styles, in.Style) || !temSlot(r.Slots, in.Slot) {
			continue
		}
		proibida := false
		for _, ing := range r.Ingredients {
			if excluded[ing.FoodID] {
				proibida = true
				break
			}
		}
		if !proibida {
			out = append(out, r)
		}
	}
	return out, nil
}

func temEstilo(styles []DietStyle, s DietStyle) bool {
	for _, v := range styles {
		if v == s {
			return true
		}
	}
	return false
}

func temSlot(slots []Slot, s Slot) bool {
	for _, v := range slots {
		if v == s {
			return true
		}
	}
	return false
}
