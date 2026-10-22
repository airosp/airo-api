package nutrition

import "testing"

func TestValueLoggedMealSomaDoCatalogo(t *testing.T) {
	xima, _ := GetFood("xima")
	matapa, _ := GetFood("matapa")

	got, err := ValueLoggedMeal([]LoggedFood{{FoodID: "xima", Grams: 250}, {FoodID: "matapa", Grams: 180}})
	if err != nil {
		t.Fatal(err)
	}
	want := xima.Kcal*2.5 + matapa.Kcal*1.8
	if diff := float64(got.Kcal) - want; diff > 0.5 || diff < -0.5 {
		t.Fatalf("kcal %d, esperava %.1f", got.Kcal, want)
	}
	if len(got.Items) != 2 || got.Items[0].FoodID != "xima" || got.Items[0].Grams != 250 {
		t.Fatalf("itens: %+v", got.Items)
	}
}

// O total arredonda-se uma vez: somar parcelas arredondadas desvia.
func TestValueLoggedMealArredondaNoFim(t *testing.T) {
	items := []LoggedFood{{"rice", 33}, {"rice", 33}, {"rice", 33}}
	got, err := ValueLoggedMeal(items)
	if err != nil {
		t.Fatal(err)
	}
	rice, _ := GetFood("rice")
	want := rice.Kcal * 0.99
	if diff := float64(got.Kcal) - want; diff > 0.5 || diff < -0.5 {
		t.Fatalf("kcal %d, esperava %.2f", got.Kcal, want)
	}
}

func TestValueLoggedMealRecusaOQueNaoExiste(t *testing.T) {
	for nome, items := range map[string][]LoggedFood{
		"vazia":            nil,
		"inventado":        {{"pizza_quatro_queijos", 100}},
		"zero gramas":      {{"xima", 0}},
		"gramas a mais":    {{"xima", MaxLoggedGrams + 1}},
		"gramas negativas": {{"xima", -10}},
	} {
		if _, err := ValueLoggedMeal(items); err == nil {
			t.Errorf("%s: devia recusar", nome)
		}
	}
}
