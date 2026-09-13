package nutrition

import "testing"

func refeicoes(kcals ...int) []PlannedMeal {
	out := make([]PlannedMeal, 0, len(kcals))
	for i, k := range kcals {
		out = append(out, PlannedMeal{
			Slot: []Slot{Breakfast, Lunch, Snack, Dinner}[i%4],
			Kcal: k,
			Macros: MacrosFloat{
				Protein: float64(k) * 0.3 / 4, Carbs: float64(k) * 0.45 / 4, Fat: float64(k) * 0.25 / 9,
			},
		})
	}
	return out
}

// Comer a mais ao almoço aperta o que falta — proporcionalmente, não em partes
// iguais: tirar igual a todas faria o lanche desaparecer antes de o jantar
// sentir alguma coisa.
func TestComerAMaisApertaOQueFalta(t *testing.T) {
	// Dia de 2 000. Comeu 1 400 quando devia ter comido 1 000; faltam lanche e
	// jantar, que estavam planeados para 1 000.
	out := Rebalance(RebalanceInput{
		DayTarget: 2000, Consumed: 1400, Remaining: refeicoes(300, 700),
	})
	if !out.Applied {
		t.Fatal("havia 400 kcal a descontar e nada foi feito")
	}
	soma := 0
	for _, m := range out.Meals {
		soma += m.Kcal
	}
	if soma != 600 {
		t.Errorf("o que resta devia somar 600 kcal, somou %d", soma)
	}
	// O jantar, que era maior, leva o maior corte.
	if out.Meals[1].Kcal >= 700 || out.Meals[0].Kcal >= 300 {
		t.Errorf("os cortes não foram aplicados: %d e %d", out.Meals[0].Kcal, out.Meals[1].Kcal)
	}
	t.Logf("lanche %d → %d · jantar %d → %d", 300, out.Meals[0].Kcal, 700, out.Meals[1].Kcal)
}

// O piso é absoluto: uma refeição de 200 kcal não é uma refeição, é um castigo.
func TestOPisoDaRefeicaoEAbsoluto(t *testing.T) {
	// Exagero: comeu quase o dia inteiro ao almoço.
	out := Rebalance(RebalanceInput{
		DayTarget: 2000, Consumed: 1900, Remaining: refeicoes(300, 700),
	})
	for _, m := range out.Meals {
		if m.Kcal < MinMealKcal {
			t.Errorf("%s ficou com %d kcal, abaixo do piso %d", m.Slot, m.Kcal, MinMealKcal)
		}
	}
	if !out.Floored {
		t.Error("o piso entrou em acção e a resposta não o disse")
	}
}

// Comer a menos dá folga ao que falta.
func TestComerAMenosDaFolga(t *testing.T) {
	out := Rebalance(RebalanceInput{
		DayTarget: 2000, Consumed: 700, Remaining: refeicoes(300, 700),
	})
	soma := 0
	for _, m := range out.Meals {
		soma += m.Kcal
	}
	if soma <= 1000 {
		t.Errorf("sobravam 1300 kcal e o plano continuou em %d", soma)
	}
}

// Sem diferença não se propõe nada — e o ecrã tem de o poder dizer em vez de
// mostrar uma proposta igual ao que já lá estava.
func TestSemDiferencaNaoSePropoeNada(t *testing.T) {
	out := Rebalance(RebalanceInput{
		DayTarget: 2000, Consumed: 1000, Remaining: refeicoes(300, 700),
	})
	if out.Applied {
		t.Error("as contas já fechavam e mesmo assim propôs mudança")
	}
}

// Sem refeições por comer não há onde distribuir.
func TestSemRefeicoesNaoRebenta(t *testing.T) {
	out := Rebalance(RebalanceInput{DayTarget: 2000, Consumed: 2400, Remaining: nil})
	if out.Applied {
		t.Error("não havia onde distribuir e disse que distribuiu")
	}
}
