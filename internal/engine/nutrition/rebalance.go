package nutrition

import "github.com/airosp/airo-api/internal/engine/portable"

// Reequilibrar o resto do dia depois de uma refeição ter corrido diferente.
//
// ⚠️ **Só a pedido.** Reequilibrar sozinho a cada registo transforma um almoço
// pesado num jantar de 300 kcal sem ninguém pedir — e a pessoa descobre pelo
// prato. A Airo propõe; quem aplica é quem come.
//
// O que **não** se faz aqui: descontar tudo de uma vez. Um excedente de 600
// kcal ao almoço não se tira do jantar, tira-se do que resta do dia, e nunca
// abaixo de um piso — uma refeição de 200 kcal não é uma refeição.

type RebalanceInput struct {
	// DayTarget é o alvo do dia.
	DayTarget int
	// Consumed é o que já foi comido, em kcal.
	Consumed int
	// Remaining são as refeições que ainda faltam, na ordem do dia.
	Remaining []PlannedMeal
}

type RebalanceResult struct {
	// Meals são as refeições que faltam, com o alvo ajustado.
	Meals []PlannedMeal
	// Delta é o que se distribuiu: negativo quando se comeu a mais.
	Delta int
	// Applied é falso quando não há nada a fazer — e o ecrã tem de o dizer em
	// vez de mostrar uma proposta igual ao que já lá estava.
	Applied bool
	// Floored diz que o piso entrou em acção: não se conseguiu descontar tudo,
	// e a interface precisa de o explicar em vez de fingir que as contas
	// fecharam.
	Floored bool
}

// MinMealKcal — abaixo disto deixa de ser uma refeição e passa a ser um castigo.
const MinMealKcal = 250

// Rebalance distribui a diferença pelas refeições que faltam.
//
// Proporcionalmente ao tamanho de cada uma: tirar igual a todas faria o lanche
// desaparecer antes de o jantar sentir alguma coisa.
func Rebalance(in RebalanceInput) RebalanceResult {
	out := RebalanceResult{Meals: in.Remaining}
	if len(in.Remaining) == 0 {
		return out
	}

	planeado := 0
	for _, m := range in.Remaining {
		planeado += m.Kcal
	}
	if planeado <= 0 {
		return out
	}

	// Quanto resta mesmo do dia, e quanto isso difere do que estava planeado.
	resta := in.DayTarget - in.Consumed
	delta := resta - planeado
	if delta == 0 {
		return out
	}

	ajustadas := make([]PlannedMeal, len(in.Remaining))
	copy(ajustadas, in.Remaining)

	/*
	 * Distribuir com piso, e **redistribuir o que o piso não deixou passar**.
	 *
	 * Repartir proporcionalmente numa só passagem não chega: a refeição pequena
	 * encosta ao piso, absorve menos do que lhe cabia, e o que sobra fica por
	 * descontar — o dia deixa de fechar e ninguém repara.
	 *
	 * Itera-se: a cada volta reparte-se o que falta pelas refeições que ainda
	 * têm folga acima do piso. Para quando fecha, ou quando já não há folga
	 * nenhuma — e aí `Floored` diz que não se conseguiu.
	 */
	alvos := make([]int, len(ajustadas))
	for i, m := range ajustadas {
		alvos[i] = m.Kcal
	}

	for volta := 0; volta < len(alvos)+1; volta++ {
		soma := 0
		for _, k := range alvos {
			soma += k
		}
		falta := resta - soma
		if falta == 0 {
			break
		}

		// Só quem tem folga acima do piso pode dar; para receber, todos servem.
		base := 0
		for _, k := range alvos {
			if falta > 0 || k > MinMealKcal {
				base += k
			}
		}
		if base == 0 {
			out.Floored = true
			break
		}

		mudou := false
		for i, k := range alvos {
			if falta < 0 && k <= MinMealKcal {
				continue
			}
			novo := k + int(portable.RoundJS(float64(falta)*float64(k)/float64(base)))
			if novo < MinMealKcal {
				novo = MinMealKcal
				out.Floored = true
			}
			if novo != k {
				alvos[i] = novo
				mudou = true
			}
		}
		if !mudou {
			out.Floored = true
			break
		}
	}

	distribuido := 0
	for i, m := range ajustadas {
		distribuido += alvos[i] - m.Kcal
		ajustadas[i].Kcal = alvos[i]
		// Os macros acompanham as calorias, mantendo a proporção da refeição.
		if m.Kcal > 0 {
			k := float64(alvos[i]) / float64(m.Kcal)
			ajustadas[i].Macros = MacrosFloat{
				Protein: portable.RoundTo(m.Macros.Protein*k, 1),
				Carbs:   portable.RoundTo(m.Macros.Carbs*k, 1),
				Fat:     portable.RoundTo(m.Macros.Fat*k, 1),
			}
		}
	}

	out.Meals = ajustadas
	out.Delta = distribuido
	out.Applied = distribuido != 0
	return out
}
