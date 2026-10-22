package evaluation

import (
	"testing"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
)

func g(v float64) *float64 { return &v }

func TestScoreContaInventadosEOmitidos(t *testing.T) {
	items := []contracts.DraftItem{
		{FoodID: "xima", Grams: nil, GramsSource: contracts.GramsMissing},
		{FoodID: "rice", Grams: g(200), GramsSource: contracts.GramsDeclared},
		{FoodID: "chicken", Grams: g(120), GramsSource: contracts.GramsDeclared},
	}
	s := ScoreDraft(items, []string{"xima", "rice", "matapa"}, map[string]float64{"rice": 150})
	if s.Hits != 2 || s.Omitted != 1 || s.Invented != 1 || s.InventedGrams != 1 {
		t.Fatalf("%+v", s)
	}
	if s.GramsError < 0.33 || s.GramsError > 0.34 {
		t.Errorf("erro das gramas %.3f", s.GramsError)
	}

	var sum Summary
	sum.Add(s)
	if p := sum.Precision(); p < 0.66 || p > 0.67 {
		t.Errorf("precisão %.2f", p)
	}
}

// As amostras só podem esperar alimentos que existem — senão medem o catálogo
// e não o modelo.
func TestAmostrasUsamOCatalogo(t *testing.T) {
	for _, c := range TextCases {
		for _, f := range c.Foods {
			if _, ok := nutrition.GetFood(f); !ok {
				t.Errorf("%q espera %q, que não está no catálogo", c.Text, f)
			}
		}
		for f := range c.Grams {
			if !contains(c.Foods, f) {
				t.Errorf("%q tem gramas para %q sem o esperar", c.Text, f)
			}
		}
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
