package openai

import (
	"context"
	"errors"
	"time"

	"github.com/airosp/airo-api/internal/intelligence"
)

// O recuo entre tentativas: começa curto e duplica, até um tecto.
const (
	backoffBase = 500 * time.Millisecond
	backoffMax  = 8 * time.Second
	// Um `Retry-After` maior do que isto não se espera dentro do pedido: a
	// execução volta à fila e outra passagem apanha-a mais tarde.
	retryAfterCap = 20 * time.Second
)

/*
 * withRetry corre `attempt` até dar certo, até falhar de vez, ou até acabarem
 * as tentativas.
 *
 * ⚠️ **Só se repete o que falhou antes de haver resposta.** Uma resposta que
 * chegou — mesmo cortada, mesmo recusada — é uma operação concluída, e pedir
 * outra vez é pagar duas vezes pela mesma coisa. Por isso só as falhas
 * passageiras voltam a correr.
 *
 * O recuo leva ruído (jitter): com várias réplicas a falhar ao mesmo tempo, um
 * recuo fixo faz todas voltarem no mesmo instante, e a segunda vaga cai como a
 * primeira.
 */
func (c *Client) withRetry(ctx context.Context, attempt func(ctx context.Context) (intelligence.StructuredResult, error)) (intelligence.StructuredResult, error) {
	var last error
	for i := 0; i <= c.cfg.MaxRetries; i++ {
		if i > 0 {
			wait := c.backoff(i, last)
			if wait > retryAfterCap {
				return intelligence.StructuredResult{}, last
			}
			c.cfg.Sleep(wait)
		}
		if err := ctx.Err(); err != nil {
			return intelligence.StructuredResult{}, err
		}

		tctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
		res, err := attempt(tctx)
		cancel()
		if err == nil {
			return res, nil
		}
		last = err
		// Cancelado por quem chamou: parar já, sem mais tentativas.
		if ctx.Err() != nil {
			return intelligence.StructuredResult{}, ctx.Err()
		}
		// O resultado volta com o erro: uma resposta cortada ou recusada
		// também gastou tokens, e o orçamento tem de os contar.
		if intelligence.KindOf(err) != intelligence.Transient {
			return res, err
		}
	}
	return intelligence.StructuredResult{}, last
}

func (c *Client) backoff(attempt int, last error) time.Duration {
	var pe *intelligence.ProviderError
	if errors.As(last, &pe) && pe.RetryAfter > 0 {
		return pe.RetryAfter
	}
	d := backoffBase << (attempt - 1)
	if d > backoffMax {
		d = backoffMax
	}
	// Entre metade e o valor inteiro.
	return d/2 + time.Duration(float64(d/2)*c.cfg.Jitter())
}
