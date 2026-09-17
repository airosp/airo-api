package training

import "sort"

/*
 * Habitos: quando uma troca repetida deixa de ser uma troca.
 *
 * ⚠️ As alterações ao treino são do dia — ao virar o dia, o treino volta ao que
 * o motor propõe. Está certo para uma dor de joelho de terça-feira; está errado
 * para quem tira o agachamento **todas as semanas** porque tem uma prótese na
 * anca. Essa pessoa passava a vida a repetir a mesma decisão, e a app nunca
 * aprendia nada com ela.
 *
 * Isto lê o que ficou guardado e diz o que já é padrão. Não decide: propõe.
 * Fixar um exercício ou excluí-lo é uma escolha com consequências no plano
 * inteiro, e quem a faz é a pessoa.
 */

// TipoDeHabito é o que se repetiu.
type TipoDeHabito string

const (
	// HabitoDeTroca — trocou sempre A por B.
	HabitoDeTroca TipoDeHabito = "swap"
	// HabitoDeRemocao — tirou A e não pôs nada no lugar.
	HabitoDeRemocao TipoDeHabito = "remove"
)

// Habito é um padrão encontrado no que a pessoa foi mudando.
type Habito struct {
	Tipo TipoDeHabito `json:"kind"`
	// De é o exercício que o motor propõe e a pessoa não quer.
	De string `json:"from"`
	// Para é o que ela põe no lugar. Vazio numa remoção.
	Para string `json:"to,omitempty"`
	// Vezes é em quantos dias distintos isto aconteceu.
	Vezes int `json:"times"`
	// PrimeiroDia é o dia mais antigo em que aconteceu, `AAAA-MM-DD`.
	PrimeiroDia string `json:"since"`
}

/*
 * VezesParaSerHabito — quantos dias fazem de uma repetição um padrão.
 *
 * Três. Duas vezes pode ser a mesma semana má; três vezes em dias diferentes é
 * uma decisão que a pessoa já tomou, e perguntar-lhe antes disso é ruído.
 */
const VezesParaSerHabito = 3

// EdicoesDeUmDia é o que ficou guardado para um dia.
type EdicoesDeUmDia struct {
	Dia     string
	Removed []string
	Swapped map[string]string
}

/*
 * HabitosEm lê os dias e devolve o que já é padrão, do mais repetido para o
 * menos.
 *
 * Conta **dias distintos** e não ocorrências: quem abre o ecrã cinco vezes no
 * mesmo dia não trocou cinco vezes.
 *
 * Uma troca e uma remoção do mesmo exercício não se somam. São decisões
 * diferentes — "quero outra coisa" e "não quero nada" — e juntá-las produziria
 * uma proposta que ninguém fez.
 */
func HabitosEm(dias []EdicoesDeUmDia) []Habito {
	type contagem struct {
		vezes    int
		primeiro string
	}
	trocas := map[[2]string]*contagem{}
	remocoes := map[string]*contagem{}

	for _, d := range dias {
		// Um exercício trocado não conta também como removido: o cliente grava
		// as duas coisas quando a troca substitui um exercício do plano.
		trocado := map[string]bool{}
		for de, para := range d.Swapped {
			if de == "" || para == "" {
				continue
			}
			trocado[de] = true
			chave := [2]string{de, para}
			if trocas[chave] == nil {
				trocas[chave] = &contagem{primeiro: d.Dia}
			}
			trocas[chave].vezes++
			if d.Dia < trocas[chave].primeiro {
				trocas[chave].primeiro = d.Dia
			}
		}
		for _, id := range d.Removed {
			if id == "" || trocado[id] {
				continue
			}
			if remocoes[id] == nil {
				remocoes[id] = &contagem{primeiro: d.Dia}
			}
			remocoes[id].vezes++
			if d.Dia < remocoes[id].primeiro {
				remocoes[id].primeiro = d.Dia
			}
		}
	}

	out := []Habito{}
	for chave, c := range trocas {
		if c.vezes >= VezesParaSerHabito {
			out = append(out, Habito{
				Tipo: HabitoDeTroca, De: chave[0], Para: chave[1],
				Vezes: c.vezes, PrimeiroDia: c.primeiro,
			})
		}
	}
	for id, c := range remocoes {
		if c.vezes >= VezesParaSerHabito {
			out = append(out, Habito{
				Tipo: HabitoDeRemocao, De: id, Vezes: c.vezes, PrimeiroDia: c.primeiro,
			})
		}
	}

	// Do mais repetido para o menos; em empate, o mais antigo primeiro — é o
	// que a pessoa anda a repetir há mais tempo.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Vezes != out[j].Vezes {
			return out[i].Vezes > out[j].Vezes
		}
		return out[i].PrimeiroDia < out[j].PrimeiroDia
	})
	return out
}
