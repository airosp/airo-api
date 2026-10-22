package evaluation

import (
	"math"

	"github.com/airosp/airo-api/internal/intelligence/contracts"
)

/*
 * Score compara um rascunho com o que se esperava.
 *
 * Quatro números, porque falham de maneiras diferentes e custam coisas
 * diferentes:
 *
 *   - **Acertos**: alimentos esperados que apareceram.
 *   - **Omitidos**: esperados que não apareceram. A pessoa acrescenta à mão —
 *     incómodo, mas visível.
 *   - **Inventados**: que apareceram sem estar lá. Pior: entram no diário se
 *     a pessoa não reparar.
 *   - **Gramas inventadas**: um alimento com quantidade que ninguém disse. No
 *     texto é sempre um erro.
 */
type Score struct {
	Hits, Omitted, Invented, InventedGrams int
	// GramsError é o erro relativo médio das quantidades ditas, de 0 a 1.
	GramsError float64
	gramsN     int
}

func ScoreDraft(items []contracts.DraftItem, wantFoods []string, wantGrams map[string]float64) Score {
	var s Score
	want := map[string]bool{}
	for _, f := range wantFoods {
		want[f] = true
	}
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.FoodID] {
			continue
		}
		seen[it.FoodID] = true
		if want[it.FoodID] {
			s.Hits++
		} else {
			s.Invented++
		}
		g, declared := wantGrams[it.FoodID]
		switch {
		case it.Grams != nil && !declared && it.GramsSource == contracts.GramsDeclared:
			s.InventedGrams++
		case it.Grams != nil && declared && g > 0:
			s.GramsError += math.Abs(*it.Grams-g) / g
			s.gramsN++
		}
	}
	for f := range want {
		if !seen[f] {
			s.Omitted++
		}
	}
	if s.gramsN > 0 {
		s.GramsError /= float64(s.gramsN)
	}
	return s
}

// Summary soma vários resultados.
type Summary struct {
	Cases                                  int
	Hits, Omitted, Invented, InventedGrams int
	gramsErrSum                            float64
	gramsCases                             int
}

func (m *Summary) Add(s Score) {
	m.Cases++
	m.Hits += s.Hits
	m.Omitted += s.Omitted
	m.Invented += s.Invented
	m.InventedGrams += s.InventedGrams
	if s.gramsN > 0 {
		m.gramsErrSum += s.GramsError
		m.gramsCases++
	}
}

// Precision é a fracção do que apareceu que estava lá.
func (m Summary) Precision() float64 { return ratio(m.Hits, m.Hits+m.Invented) }

// Recall é a fracção do que estava lá que apareceu.
func (m Summary) Recall() float64 { return ratio(m.Hits, m.Hits+m.Omitted) }

// GramsError é o erro relativo médio das quantidades ditas.
func (m Summary) GramsError() float64 {
	if m.gramsCases == 0 {
		return 0
	}
	return m.gramsErrSum / float64(m.gramsCases)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}
