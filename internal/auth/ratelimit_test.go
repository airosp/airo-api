package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Redis a sério (miniredis), não um mock: o script corre em Lua dentro do
// servidor, e um mock testaria a minha ideia do que o Lua faz.
func limiter(t *testing.T) (*RedisLimiter, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	l := NewRedisLimiter(rdb)
	// Relógio controlado: testar uma janela de uma hora não pode exigir esperar
	// uma hora.
	fake := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return fake }
	return l, s
}

func advance(l *RedisLimiter, d time.Duration) {
	at := l.now().Add(d)
	l.now = func() time.Time { return at }
}

func TestLimitAllowsUpToMaxThenRefuses(t *testing.T) {
	l, _ := limiter(t)
	ctx := context.Background()
	limit := Limit{Max: 5, Window: time.Hour}

	for i := 1; i <= 5; i++ {
		g, err := l.Allow(ctx, "phone:+258841234567", limit)
		if err != nil {
			t.Fatal(err)
		}
		if !g.Allowed {
			t.Fatalf("pedido %d devia passar", i)
		}
	}
	g, err := l.Allow(ctx, "phone:+258841234567", limit)
	if err != nil {
		t.Fatal(err)
	}
	if g.Allowed {
		t.Fatal("o sexto devia ser recusado")
	}
	if g.RetryAfter <= 0 || g.RetryAfter > time.Hour {
		t.Fatalf("retry-after %v", g.RetryAfter)
	}
	t.Logf("5 passaram, o 6.º recusado com retry-after de %v", g.RetryAfter.Round(time.Minute))
}

// A janela é **deslizante**, não fixa.
//
// Com janelas fixas, cinco pedidos no último segundo de uma hora e cinco no
// primeiro da seguinte passam os dez — e o limite de cinco por hora torna-se dez
// em dois segundos.
//
// Os pedidos são espalhados no tempo de propósito: feitos todos no mesmo
// instante expiram todos juntos, e aí a diferença entre deslizante e fixa não
// aparece. Foi assim que escrevi o teste à primeira, e passou a dizer o que não
// devia.
func TestWindowSlidesInsteadOfResetting(t *testing.T) {
	l, _ := limiter(t)
	ctx := context.Background()
	limit := Limit{Max: 5, Window: time.Hour}

	// Cinco pedidos, um a cada dez minutos: t=0, 10, 20, 30, 40.
	for i := 0; i < 5; i++ {
		if g, _ := l.Allow(ctx, "k", limit); !g.Allowed {
			t.Fatalf("pedido %d (aos %d min)", i, i*10)
		}
		advance(l, 10*time.Minute)
	}
	// Agora t=50: os cinco estão todos dentro da janela.
	if g, _ := l.Allow(ctx, "k", limit); g.Allowed {
		t.Fatal("aos 50 minutos os cinco ainda contam")
	}

	// t=61: só o primeiro (t=0) saiu da janela. Abre **uma** vaga.
	advance(l, 11*time.Minute)
	if g, _ := l.Allow(ctx, "k", limit); !g.Allowed {
		t.Fatal("aos 61 minutos o primeiro já saiu — devia abrir uma vaga")
	}
	if g, _ := l.Allow(ctx, "k", limit); g.Allowed {
		t.Fatal("abriu uma vaga, não duas: os outros quatro ainda contam")
	}

	// t=71: sai o segundo (t=10), abre outra.
	advance(l, 10*time.Minute)
	if g, _ := l.Allow(ctx, "k", limit); !g.Allowed {
		t.Fatal("aos 71 minutos o segundo já saiu")
	}
	if g, _ := l.Allow(ctx, "k", limit); g.Allowed {
		t.Fatal("uma de cada vez, à medida que saem")
	}
}

// Chaves diferentes não se contaminam.
func TestKeysAreIndependent(t *testing.T) {
	l, _ := limiter(t)
	ctx := context.Background()
	limit := Limit{Max: 2, Window: time.Hour}

	for i := 0; i < 2; i++ {
		l.Allow(ctx, "phone:a", limit)
	}
	if g, _ := l.Allow(ctx, "phone:a", limit); g.Allowed {
		t.Fatal("a devia estar travado")
	}
	if g, _ := l.Allow(ctx, "phone:b", limit); !g.Allowed {
		t.Fatal("b não tem nada a ver com a")
	}
}

// Os cinco eixos, e o global primeiro.
func TestGuardChecksEveryAxis(t *testing.T) {
	ctx := context.Background()
	req := OTPRequest{
		PhoneE164: "+258841234567", CountryCode: "MZ",
		IPHash: "abc123", DeviceID: "device-1",
	}

	cases := []struct {
		name   string
		limits Limits
		axis   string
	}{
		{"número por hora", Limits{PerPhoneHour: Limit{Max: 1, Window: time.Hour}}, "phone_hour"},
		{"número por dia", Limits{PerPhoneDay: Limit{Max: 1, Window: 24 * time.Hour}}, "phone_day"},
		{"IP", Limits{PerIPHour: Limit{Max: 1, Window: time.Hour}}, "ip"},
		{"dispositivo", Limits{PerDeviceDay: Limit{Max: 1, Window: 24 * time.Hour}}, "device"},
		{"prefixo de país", Limits{PerCountryHour: Limit{Max: 1, Window: time.Hour}}, "country"},
		{"global", Limits{GlobalHour: Limit{Max: 1, Window: time.Hour}}, "global"},
	}

	for _, tc := range cases {
		l, _ := limiter(t)
		first, err := Guard(ctx, l, tc.limits, req)
		if err != nil || !first.Allowed {
			t.Fatalf("%s: o primeiro devia passar (%v, %v)", tc.name, first, err)
		}
		second, err := Guard(ctx, l, tc.limits, req)
		if err != nil {
			t.Fatal(err)
		}
		if second.Allowed {
			t.Errorf("%s: o segundo devia ser travado", tc.name)
			continue
		}
		if second.Axis != tc.axis {
			t.Errorf("%s: travou em %q", tc.name, second.Axis)
		}
	}
}

// ⚠️ **O disjuntor de orçamento.**
//
// É o que falta na maioria das implementações: quando o gasto horário passa o
// limiar, **para de enviar** e alerta, em vez de continuar até a factura
// chegar. E dispara mesmo com todos os outros eixos folgados.
func TestGlobalCircuitBreakerTripsBeforeAnythingElse(t *testing.T) {
	l, _ := limiter(t)
	ctx := context.Background()

	limits := DefaultLimits()
	limits.GlobalHour = Limit{Max: 3, Window: time.Hour} // orçamento apertado

	// Três números diferentes: nenhum eixo individual chega perto do limite.
	for i := 0; i < 3; i++ {
		req := OTPRequest{
			PhoneE164:   fmt.Sprintf("+25884000000%d", i),
			CountryCode: "MZ", IPHash: fmt.Sprintf("ip%d", i), DeviceID: fmt.Sprintf("d%d", i),
		}
		if d, _ := Guard(ctx, l, limits, req); !d.Allowed {
			t.Fatalf("pedido %d travou em %q", i, d.Axis)
		}
	}

	// O quarto, de um número que nunca pediu nada, é travado pelo orçamento.
	fresh := OTPRequest{PhoneE164: "+258849999999", CountryCode: "MZ", IPHash: "novo", DeviceID: "novo"}
	d, err := Guard(ctx, l, limits, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("o orçamento acabou e continuou a enviar")
	}
	if d.Axis != "global" {
		t.Fatalf("travou em %q, devia ser o disjuntor", d.Axis)
	}
	t.Logf("orçamento esgotado: travado em %q, retry em %v", d.Axis, d.RetryAfter.Round(time.Minute))
}

// Um eixo sem valor não trava nem conta: recusar por falta de IP poria de fora
// quem usa uma rede que não o expõe.
func TestMissingAxisDoesNotBlock(t *testing.T) {
	l, _ := limiter(t)
	ctx := context.Background()
	limits := DefaultLimits()

	req := OTPRequest{PhoneE164: "+258841234567", CountryCode: "MZ"} // sem IP nem dispositivo
	for i := 0; i < 5; i++ {
		if d, err := Guard(ctx, l, limits, req); err != nil || !d.Allowed {
			t.Fatalf("pedido %d: %v %v", i, d, err)
		}
	}
}

// Sem Redis, o limite não pode falhar aberto: um erro de infra-estrutura não
// pode transformar-se em envios ilimitados.
func TestLimiterFailsClosed(t *testing.T) {
	l, srv := limiter(t)
	srv.Close()

	g, err := l.Allow(context.Background(), "k", Limit{Max: 5, Window: time.Hour})
	if err == nil {
		t.Fatal("devia devolver erro")
	}
	if g.Allowed {
		t.Fatal("com o Redis em baixo, o limite não pode deixar passar")
	}
}
