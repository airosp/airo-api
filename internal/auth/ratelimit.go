package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ⚠️ **Não é o código que protege a conta; são os limites que protegem o
// sistema.**
//
// Cada OTP custa dinheiro: a Meta cobra por conversa de autenticação. Um
// atacante que peça códigos em massa não rouba contas — **esvazia o
// orçamento**. É a ameaça mais provável, e trata-se com limites, não com
// validação de código.

var ErrRateLimited = errors.New("limite excedido")

type Limit struct {
	// Max pedidos dentro da janela.
	Max    int
	Window time.Duration
}

// Limits são os cinco eixos de docs/backend/08-autenticacao.md §5.
type Limits struct {
	// PerPhone protege quem está a ser assediado com códigos.
	PerPhoneHour Limit
	PerPhoneDay  Limit
	// PerIP trava o varrimento.
	PerIPHour Limit
	// PerDevice trava a automação.
	PerDeviceDay Limit
	// PerCountry trava fraude de tarifação dirigida a um prefixo.
	PerCountryHour Limit
	// GlobalHour é o disjuntor de orçamento — o que falta na maioria das
	// implementações.
	GlobalHour Limit
}

func DefaultLimits() Limits {
	return Limits{
		PerPhoneHour:   Limit{Max: 5, Window: time.Hour},
		PerPhoneDay:    Limit{Max: 10, Window: 24 * time.Hour},
		PerIPHour:      Limit{Max: 20, Window: time.Hour},
		PerDeviceDay:   Limit{Max: 10, Window: 24 * time.Hour},
		PerCountryHour: Limit{Max: 500, Window: time.Hour},
		GlobalHour:     Limit{Max: 2000, Window: time.Hour},
	}
}

// Limiter permite e consome, e diz quando será permitido de novo.
type Limiter interface {
	Allow(ctx context.Context, key string, limit Limit) (ok bool, retryAfter time.Duration, err error)
}

// RedisLimiter é uma janela deslizante em Redis.
//
// ⚠️ **Nunca em memória.** Com duas instâncias, um limite de 5 por hora passa a
// 10 — e o atacante só tem de alternar entre elas.
//
// A janela é deslizante e não fixa: com janelas fixas, dez pedidos no último
// segundo de uma hora e dez no primeiro da seguinte passam os dois, e o limite
// de dez por hora torna-se vinte em dois segundos.
type RedisLimiter struct {
	rdb redis.UniversalClient
	now func() time.Time
}

func NewRedisLimiter(rdb redis.UniversalClient) *RedisLimiter {
	return &RedisLimiter{rdb: rdb, now: time.Now}
}

// slidingWindow é atómico de propósito: entre ler e escrever, um segundo pedido
// passaria sem contar.
//
//	KEYS[1] chave    ARGV[1] agora (ns)   ARGV[2] janela (ns)   ARGV[3] máximo
const slidingWindow = `
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local max    = tonumber(ARGV[3])

redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
local used = redis.call('ZCARD', key)

if used >= max then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local retry = window
  if oldest[2] then retry = (tonumber(oldest[2]) + window) - now end
  if retry < 0 then retry = 0 end
  return {0, retry}
end

redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
redis.call('PEXPIRE', key, math.ceil(window / 1000000))
return {1, 0}
`

func (l *RedisLimiter) Allow(ctx context.Context, key string, limit Limit) (bool, time.Duration, error) {
	if limit.Max <= 0 || limit.Window <= 0 {
		return true, 0, nil
	}
	now := l.now().UnixNano()

	res, err := l.rdb.Eval(ctx, slidingWindow, []string{"rl:" + key},
		now, limit.Window.Nanoseconds(), limit.Max).Result()
	if err != nil {
		return false, 0, fmt.Errorf("limite: %w", err)
	}

	values, ok := res.([]any)
	if !ok || len(values) != 2 {
		return false, 0, fmt.Errorf("limite: resposta inesperada %v", res)
	}
	allowed, _ := values[0].(int64)
	retry, _ := values[1].(int64)
	return allowed == 1, time.Duration(retry), nil
}

// OTPRequest é o pedido a avaliar contra os cinco eixos.
type OTPRequest struct {
	PhoneE164   string
	CountryCode string
	IPHash      string
	DeviceID    string
}

// Decision diz se pode seguir, e porquê não.
type Decision struct {
	Allowed bool
	// Axis é o eixo que travou — para o registo, nunca para a resposta: dizer
	// à pessoa **qual** limite bateu diz ao atacante o que contornar.
	Axis       string
	RetryAfter time.Duration
}

// Guard avalia um pedido de código contra todos os eixos.
//
// A ordem importa: o global primeiro. Quando o orçamento acaba, não vale a pena
// gastar viagens ao Redis a verificar os outros — e o disjuntor tem de disparar
// antes de qualquer envio.
func Guard(ctx context.Context, l Limiter, limits Limits, req OTPRequest) (Decision, error) {
	checks := []struct {
		axis  string
		key   string
		limit Limit
	}{
		{"global", "global", limits.GlobalHour},
		{"phone_hour", "phone:" + req.PhoneE164, limits.PerPhoneHour},
		{"phone_day", "phoned:" + req.PhoneE164, limits.PerPhoneDay},
		{"country", "country:" + req.CountryCode, limits.PerCountryHour},
		{"ip", "ip:" + req.IPHash, limits.PerIPHour},
		{"device", "device:" + req.DeviceID, limits.PerDeviceDay},
	}

	for _, c := range checks {
		// Um eixo sem valor — sem IP, sem dispositivo — não trava nem conta.
		// Recusar por falta de dados poria de fora quem usa uma rede que não
		// expõe o IP.
		if c.key == c.axis+":" || c.limit.Max <= 0 {
			continue
		}
		ok, retry, err := l.Allow(ctx, c.key, c.limit)
		if err != nil {
			return Decision{}, err
		}
		if !ok {
			return Decision{Allowed: false, Axis: c.axis, RetryAfter: retry}, nil
		}
	}
	return Decision{Allowed: true}, nil
}
