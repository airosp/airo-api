package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/migrations"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// ⚠️ O teste que faltava.
//
// Construí todos os handlers e nunca os liguei ao router: o `main` montava a
// API sem dependência nenhuma, e as rotas só se registam quando elas chegam. A
// API subia, respondia `/healthz`, e devolvia **404 a tudo o resto** — sem nada
// a dizer que faltava alguma coisa.
//
// Isto percorre as rotas que a API tem de ter e recusa um 404 em qualquer uma.
func TestWiredAPIRegistersEveryRoute(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(ctx, pool, migs, quietLogger()); err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	secret := make([]byte, 32)
	pepper := make([]byte, 32)
	for i := range secret {
		secret[i], pepper[i] = byte(i+1), byte(i+100)
	}

	log := quietLogger()
	deps := airohttp.Wire(airohttp.Platform{
		Log: log, Version: "test", Pool: pool, Redis: rdb,
		Clock:     clock.NewFixed(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)),
		JWTSecret: secret, OTPPepper: pepper,
		Sender: airohttp.NewLogSender(log),
	})
	deps.Schema = airohttp.SchemaState{Migrations: migs, Pool: pool}
	router := airohttp.NewRouter(deps)

	routes := []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/readyz"},
		{"POST", "/v1/auth/otp/request"},
		{"POST", "/v1/auth/otp/verify"},
		{"POST", "/v1/auth/token/refresh"},
		{"POST", "/v1/goals"},
		{"POST", "/v1/goals/assess"},
		{"GET", "/v1/training/today"},
		{"POST", "/v1/training/sessions"},
	}

	for _, route := range routes {
		r := httptest.NewRequest(route.method, route.path, http.NoBody)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		// 404 é o único resultado inaceitável: qualquer outro quer dizer que a
		// rota existe e está a fazer o seu trabalho — mesmo que recuse o pedido.
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s → 404: a rota não está registada", route.method, route.path)
		}
	}

	// E as privadas exigem mesmo autenticação.
	for _, route := range routes[2:] {
		if route.path == "/v1/auth/otp/request" || route.path == "/v1/auth/otp/verify" ||
			route.path == "/v1/auth/token/refresh" {
			continue
		}
		r := httptest.NewRequest(route.method, route.path, http.NoBody)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sem token → %d, devia ser 401", route.method, route.path, w.Code)
		}
	}
	t.Logf("%d rotas registadas", len(routes))
}

// Sem segredo de assinatura, as rotas privadas **não existem** — em vez de
// existirem sem protecção.
func TestWithoutSecretsPrivateRoutesAreNotRegistered(t *testing.T) {
	pool := pgtest.Pool(t)
	log := quietLogger()
	deps := airohttp.Wire(airohttp.Platform{
		Log: log, Version: "test", Pool: pool,
		Clock: clock.NewFixed(time.Now()),
	})
	router := airohttp.NewRouter(deps)

	r := httptest.NewRequest("POST", "/v1/goals", http.NoBody)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("sem segredo, /v1/goals devia não existir; deu %d", w.Code)
	}
}
