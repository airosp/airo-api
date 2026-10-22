package openai

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/intelligence"
)

/*
 * classify traduz uma resposta de erro numa falha com destino.
 *
 * A pergunta não é "o que correu mal" mas "vale a pena tentar outra vez":
 *
 *   - 429 por ritmo, 5xx e 408: sim, depois de esperar.
 *   - 429 por saldo (`insufficient_quota`): não — a conta está sem dinheiro, e
 *     insistir só enche o registo.
 *   - 400, 401, 403, 404, 422: não — o pedido, a chave ou o modelo estão
 *     errados, e continuarão errados na próxima tentativa.
 *
 * Do corpo só sai o código e a mensagem, cortada. Um corpo de erro pode
 * repetir parte do pedido, e o pedido leva dados de alguém.
 */
func classify(status int, header http.Header, body []byte) *intelligence.ProviderError {
	pe := &intelligence.ProviderError{Status: status, Kind: intelligence.Permanent}
	var env errorEnvelope
	if json.Unmarshal(body, &env) == nil && env.Error != nil {
		pe.Code = env.Error.Code
		if pe.Code == "" {
			pe.Code = env.Error.Type
		}
		pe.Msg = cut(env.Error.Message, 200)
	}
	if pe.Msg == "" {
		pe.Msg = http.StatusText(status)
	}

	switch {
	case status == http.StatusTooManyRequests && (pe.Code == "insufficient_quota" || strings.Contains(pe.Msg, "quota")):
		pe.Kind = intelligence.Quota
	case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout, status >= 500:
		pe.Kind = intelligence.Transient
		pe.RetryAfter = retryAfter(header)
	}
	return pe
}

// retryAfter lê o tempo de espera que o fornecedor pede, em segundos ou em
// milissegundos. Ausente é zero, e quem tenta outra vez usa o próprio recuo.
func retryAfter(h http.Header) time.Duration {
	if v := h.Get("retry-after-ms"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(v); err == nil && s > 0 {
			return time.Duration(s) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	return 0
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
