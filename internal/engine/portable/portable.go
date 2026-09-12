// Package portable tem as duas operações onde Go e TypeScript divergem.
//
// Não é uma biblioteca de utilidades: é o sítio onde ficam as diferenças que já
// morderam, para que mordam uma vez só. Todos os motores portados passam por
// aqui em vez de reimplementarem `%` e `Math.round` à sua maneira.
//
// Ver docs/backend/04-arquitetura-go.md, "Porte de TypeScript para Go".
package portable

import "math"

// RoundJS arredonda como o JavaScript: meio **para cima**, sempre.
//
// `math.Round` arredonda meio **para longe de zero**, e os dois só concordam
// em positivos. Verificado nos dois runtimes:
//
//	         JS        Go (math.Round)
//	-0.5     -0        -1
//	-1.5     -1        -2
//	-2.5     -2        -3
//
// Onde o valor pode ser negativo — uma variação de peso, um défice calórico,
// um desvio face ao previsto — usar `math.Round` dá um resultado diferente do
// que o cliente mostra hoje, e a diferença aparece como um quilo a mais ou a
// menos sem explicação.
func RoundJS(x float64) float64 {
	return math.Floor(x + 0.5)
}

// Mod é o resto sempre não-negativo.
//
// `%` devolve o sinal do dividendo nas duas linguagens: `-7 % 3 == -1` em Go e
// em TypeScript. A diferença está no que acontece a seguir: em TypeScript
// `array[-1]` é `undefined` e o código segue em frente com um buraco; em Go,
// `slice[-1]` entra em pânico e leva o pedido inteiro.
//
// O problema já existe hoje em TypeScript — é silencioso, não ausente.
func Mod(n, m int) int {
	if m <= 0 {
		return 0
	}
	return ((n % m) + m) % m
}

// Pick escolhe `count` elementos de forma estável a partir de uma semente.
//
// É o porte de `pick()` do workout-engine. Duas propriedades que os testes
// fixam: a mesma semente dá sempre a mesma escolha (é o que faz o treino de um
// dia ser igual em dois dispositivos), e sementes negativas não entram em
// pânico.
//
// O passo de 13 não é decorativo. Com um passo que divida o tamanho da lista, a
// escolha fica presa nos mesmos índices — já aconteceu: passo 7 sobre 7
// vegetais fixava o vegetal para sempre. 13 é primo, e por isso percorre a
// lista toda para qualquer tamanho que não seja múltiplo dele.
func Pick[T any](items []T, count int, seed int) []T {
	if count <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= count {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}

	chosen := make([]T, 0, count)
	taken := make(map[int]bool, count)
	for i := 0; len(chosen) < count && i < len(items)*3; i++ {
		at := Mod(seed+i*13, len(items))
		if taken[at] {
			continue
		}
		taken[at] = true
		chosen = append(chosen, items[at])
	}
	return chosen
}

// SeedFrom é o hash estável usado para tornar o treino do dia reproduzível.
//
// Porte directo de `seedFrom` em build-session.ts: `hash * 31 + charCode`, com
// truncagem a 32 bits com sinal (o `| 0` do JavaScript) e valor absoluto no
// fim. A truncagem tem de ser explícita — em Go um `int` é de 64 bits e sem
// ela a semente divergiria da do cliente a partir de cadeias longas.
func SeedFrom(text string) int {
	var hash int32
	for _, r := range text {
		hash = hash*31 + int32(r)
	}
	if hash < 0 {
		// Math.abs sobre o mínimo de 32 bits não cabe em int32; alarga-se
		// antes de negar, como o JavaScript faz ao trabalhar em float64.
		return int(-int64(hash))
	}
	return int(hash)
}
