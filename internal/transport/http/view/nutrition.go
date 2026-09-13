package view

import (
	"fmt"
	"strings"

	"github.com/airosp/airo-api/internal/engine/nutrition"
)

// ── O plano alimentar do dia ─────────────────────────────────────────────────
//
// O mesmo princípio do pacote da sessão: o cliente desenha, não calcula. Os
// subtítulos, as percentagens e o texto que explica de onde vem o alvo vêm
// decididos daqui.

type MacroTargets struct {
	ProteinG int `json:"proteinG"`
	CarbsG   int `json:"carbsG"`
	FatG     int `json:"fatG"`
}

type MealItemView struct {
	FoodID string `json:"foodId"`
	Name   string `json:"name"`
	Grams  int    `json:"grams"`
	Kcal   int    `json:"kcal"`
	// Macros da porção, com uma casa: é o que o cartão de detalhe mostra.
	ProteinG float64 `json:"proteinG"`
	CarbsG   float64 `json:"carbsG"`
	FatG     float64 `json:"fatG"`
}

type MealView struct {
	ID    string `json:"id"`
	Slot  string `json:"slot"`
	Title string `json:"title"`
	// Subtitle é a lista de alimentos, já escrita. O cliente lia-a juntando
	// nomes com vírgulas — e uma refeição vazia ficava com um subtítulo em
	// branco em vez de dizer o que era.
	Subtitle string `json:"subtitle"`
	// Role explica a refeição: antes do treino, depois, leve. O cliente mostra
	// a etiqueta; não a deduz da hora.
	Role      string `json:"role"`
	RoleLabel string `json:"roleLabel,omitempty"`
	// Swapped marca a refeição que a pessoa trocou. O ecrã mostra-a como
	// escolha dela e oferece o caminho de volta — sem isto, "repor" aparecia
	// em refeições que ninguém tinha mexido.
	Swapped bool `json:"swapped,omitempty"`
	Kcal    int  `json:"kcal"`
	// Share é a fatia do dia, de 0 a 1 — já calculada, para a barra não ter de
	// dividir por um total que pode ser zero.
	Share  float64        `json:"share"`
	Macros MacroTargets   `json:"macros"`
	Items  []MealItemView `json:"items"`
}

type NutritionDay struct {
	DayISO        string       `json:"dayISO"`
	CalorieTarget int          `json:"calorieTarget"`
	Macros        MacroTargets `json:"macros"`
	Meals         []MealView   `json:"meals"`
	// Basis diz de onde veio o alvo, em linguagem de quem lê.
	Basis string `json:"basis"`
	// TrainsToday muda o que as refeições em torno do treino fazem, e o ecrã
	// diz porquê.
	TrainsToday bool `json:"trainsToday"`
}

var roleLabels = map[string]string{
	nutrition.RolePreWorkout:  "Antes do treino",
	nutrition.RolePostWorkout: "Depois do treino",
	nutrition.RoleLight:       "Leve",
}

// BuildNutritionDay monta a resposta que o ecrã de nutrição desenha.
func BuildNutritionDay(s nutrition.Strategy2, d nutrition.DayPlan, trainsToday, stored bool, swapped map[string]bool) NutritionDay {
	out := NutritionDay{
		DayISO:        d.DayISO,
		CalorieTarget: d.Kcal,
		Macros: MacroTargets{
			ProteinG: d.Macros.Protein, CarbsG: d.Macros.Carbs, FatG: d.Macros.Fat,
		},
		Meals:       make([]MealView, 0, len(d.Meals)),
		Basis:       basisText(s, stored),
		TrainsToday: trainsToday,
	}

	for _, m := range d.Meals {
		share := 0.0
		if d.Kcal > 0 {
			share = round4(float64(m.Kcal) / float64(d.Kcal))
		}
		view := MealView{
			ID:        m.ID,
			Slot:      string(m.Slot),
			Title:     m.Title,
			Subtitle:  subtitleOf(m),
			Role:      m.Role,
			RoleLabel: roleLabels[m.Role],
			Swapped:   swapped[string(m.Slot)],
			Kcal:      m.Kcal,
			Share:     share,
			Macros: MacroTargets{
				ProteinG: int(m.Macros.Protein + 0.5),
				CarbsG:   int(m.Macros.Carbs + 0.5),
				FatG:     int(m.Macros.Fat + 0.5),
			},
			Items: make([]MealItemView, 0, len(m.Items)),
		}
		for _, it := range m.Items {
			view.Items = append(view.Items, MealItemView{
				FoodID: it.FoodID, Name: it.Name,
				Grams: int(it.Grams), Kcal: int(it.Kcal),
				ProteinG: it.Macros.Protein, CarbsG: it.Macros.Carbs, FatG: it.Macros.Fat,
			})
		}
		out.Meals = append(out.Meals, view)
	}
	return out
}

// subtitleOf — os alimentos da refeição, por extenso.
//
// Uma refeição sem itens diz "Refeição livre" e não fica em branco: um cartão
// vazio parece um erro de carregamento.
func subtitleOf(m nutrition.PlannedMeal) string {
	if len(m.Items) == 0 {
		return "Refeição livre"
	}
	names := make([]string, 0, len(m.Items))
	for _, it := range m.Items {
		names = append(names, it.Name)
	}
	return strings.Join(names, ", ")
}

// basisText explica o número em vez de o apresentar sozinho.
//
// Um alvo calórico sem explicação é um número que ninguém segue — e a diferença
// entre o gasto que o corpo revelou e o que a fórmula previu é precisamente o
// que dá confiança ao valor.
func basisText(s nutrition.Strategy2, stored bool) string {
	if stored {
		return fmt.Sprintf("Do teu plano · gasto estimado %d kcal", s.BasisTDEE)
	}
	if s.BasisSource == "observed" {
		return fmt.Sprintf("Do teu registo · gasto observado %d kcal", s.BasisTDEE)
	}
	return fmt.Sprintf("Estimativa · gasto %d kcal. Cria o teu objectivo para afinar.", s.BasisTDEE)
}
