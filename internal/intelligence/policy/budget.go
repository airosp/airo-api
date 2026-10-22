package policy

import (
	"math"
	"time"
)

/*
 * Prices são os preços do modelo em uso, em dólares por milhão de tokens.
 *
 * ⚠️ Vêm da configuração e não do código: mudam sem aviso, dependem do modelo,
 * e um preço escrito aqui ficava certo até ao dia em que deixava de estar —
 * sem nada a dizê-lo. Quem liga a IA consulta a tabela oficial e escreve-os.
 */
type Prices struct {
	InputPerMTokUSD  float64
	OutputPerMTokUSD float64
}

// Configured diz se há preço para calcular o custo. Sem ele não há orçamento
// que se possa fazer cumprir, e a IA não arranca.
func (p Prices) Configured() bool { return p.InputPerMTokUSD > 0 && p.OutputPerMTokUSD > 0 }

// CostMicroUSD é o custo de uma chamada, em milionésimos de dólar.
//
//	custo = (tokens_entrada × preço_entrada + tokens_saída × preço_saída) / 10⁶
//
// Em inteiros e arredondado para cima: o orçamento mede o que se gastou, e um
// arredondamento para baixo repetido mil vezes é dinheiro gasto sem constar.
func (p Prices) CostMicroUSD(inputTokens, outputTokens int64) int64 {
	usd := (float64(inputTokens)*p.InputPerMTokUSD + float64(outputTokens)*p.OutputPerMTokUSD) / 1e6
	return int64(math.Ceil(usd * 1e6))
}

// Budget são os limites de gasto.
type Budget struct {
	// MonthlyMicroUSD é o tecto do mês, para a instalação inteira.
	MonthlyMicroUSD int64
	// DailyPerUser é quantas análises cada pessoa pode pedir por dia, por
	// funcionalidade. Uma fotografia e um texto contam em separado.
	DailyPerUser int
	// AlertRatio é a fracção do orçamento a partir da qual se avisa nos
	// registos — antes de acabar, e não no momento em que acaba.
	AlertRatio float64
}

// MonthStart é o início do mês, em UTC, de onde se soma o gasto.
func MonthStart(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// DayStart é o início do dia, em UTC, de onde se contam os pedidos de alguém.
//
// UTC e não o dia local: é um limite de gasto, não uma regra de domínio, e um
// limite que muda de fronteira com o fuso de quem pede deixava dar a volta ao
// limite mudando o fuso do telemóvel.
func DayStart(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// Exhausted diz se o mês já não comporta mais nenhuma chamada.
func (b Budget) Exhausted(spentMicroUSD int64) bool {
	return b.MonthlyMicroUSD > 0 && spentMicroUSD >= b.MonthlyMicroUSD
}

// NearLimit diz se se passou o limiar de aviso.
func (b Budget) NearLimit(spentMicroUSD int64) bool {
	if b.MonthlyMicroUSD <= 0 || b.AlertRatio <= 0 {
		return false
	}
	return float64(spentMicroUSD) >= float64(b.MonthlyMicroUSD)*b.AlertRatio
}
