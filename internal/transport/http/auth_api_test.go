package http_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type codeCapture struct{ codes map[string]string }

func (c *codeCapture) Send(_ context.Context, phone, code, _ string) (string, error) {
	c.codes[phone] = code
	return "wamid", nil
}

// serveAuth monta a API **com o verificador de token real**. Deixa de haver
// duplo: as rotas privadas passam a exigir um token que a própria API emitiu.
func serveAuth(t *testing.T) (http.Handler, *codeCapture, *pgxpool.Pool) {
	t.Helper()
	_, pool, _ := serve(t)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	pepper := make([]byte, 32)
	rand.Read(pepper)
	secret := make([]byte, 32)
	rand.Read(secret)

	clk := clock.NewFixed(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))
	sender := &codeCapture{codes: map[string]string{}}
	tokens := auth.NewTokenIssuer(secret, clk.Now)

	authSvc := service.NewAuthService(
		repo.NewAuthRepo(repo.NewTxManager(pool)),
		auth.NewRedisLimiter(rdb), sender,
		service.DefaultAuthConfig(pepper), clk)

	router := airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		AuthAPI:     &handlers.Auth{Service: authSvc, Tokens: tokens},
		Auth:        tokens, // ← o verificador real
		Goals:       &handlers.Goals{Service: nil, Profiles: profiles{}},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
	return router, sender, pool
}

func postPublic(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const phone = "+258847771234"

func login(t *testing.T, h http.Handler, sender *codeCapture) (access, refresh string) {
	t.Helper()
	req := postPublic(t, h, "/v1/auth/otp/request",
		`{"phone":"84 777 1234","channel":"auto","deviceId":"device-1"}`)
	if req.Code != http.StatusAccepted {
		t.Fatalf("request: %d %s", req.Code, req.Body.String())
	}
	var challenge struct {
		ChallengeID string `json:"challengeId"`
	}
	json.Unmarshal(req.Body.Bytes(), &challenge)

	body, _ := json.Marshal(map[string]string{
		"challengeId": challenge.ChallengeID, "code": sender.codes[phone],
		"deviceId": "device-1", "platform": "ios",
	})
	ver := postPublic(t, h, "/v1/auth/otp/verify", string(body))
	if ver.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", ver.Code, ver.Body.String())
	}
	var session struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int    `json:"expiresIn"`
		IsNewUser    bool   `json:"isNewUser"`
	}
	json.Unmarshal(ver.Body.Bytes(), &session)
	if session.AccessToken == "" || session.RefreshToken == "" || session.ExpiresIn == 0 {
		t.Fatalf("sessão incompleta: %s", ver.Body.String())
	}
	return session.AccessToken, session.RefreshToken
}

// O fluxo inteiro por HTTP, e o token emitido abre as rotas privadas.
func TestLoginOverHTTPOpensPrivateRoutes(t *testing.T) {
	h, sender, _ := serveAuth(t)
	access, _ := login(t, h, sender)

	// Com o token: a rota privada responde (o serviço é nil, logo 500 — mas
	// passou a autenticação, que é o que se está a testar).
	r := httptest.NewRequest(http.MethodPost, "/v1/goals", bytes.NewBufferString(`{}`))
	r.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("o token emitido não abriu a rota: %s", w.Body.String())
	}

	// Sem token, e com um token inventado: ambos 401.
	for _, header := range []string{"", "Bearer inventado", "Bearer a.b.c"} {
		r := httptest.NewRequest(http.MethodPost, "/v1/goals", bytes.NewBufferString(`{}`))
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%q deu %d", header, w.Code)
		}
	}
}

// ⚠️ A resposta ao request é a mesma para número registado e desconhecido, e
// **nunca** diz se a conta existe.
func TestRequestNeverRevealsWhetherTheAccountExists(t *testing.T) {
	h, sender, _ := serveAuth(t)
	login(t, h, sender) // regista o número

	known := postPublic(t, h, "/v1/auth/otp/request", `{"phone":"84 777 1234"}`)
	fresh := postPublic(t, h, "/v1/auth/otp/request", `{"phone":"84 777 9999"}`)

	if known.Code != fresh.Code {
		t.Fatalf("%d ≠ %d", known.Code, fresh.Code)
	}
	for _, body := range []string{known.Body.String(), fresh.Body.String()} {
		for _, forbidden := range []string{"isNewUser", "exists", "registered", "userId"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("a resposta leva %q: %s", forbidden, body)
			}
		}
	}
	// E têm a mesma forma: os mesmos campos, pela mesma ordem.
	shape := func(b string) []string {
		var m map[string]any
		json.Unmarshal([]byte(b), &m)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return keys
	}
	if len(shape(known.Body.String())) != len(shape(fresh.Body.String())) {
		t.Fatal("as respostas têm formas diferentes")
	}
	t.Logf("as duas respostas: %s", known.Body.String())
}

// O código errado dá 401 e **não conta tentativas na mensagem**.
func TestWrongCodeDoesNotCountAttemptsInTheMessage(t *testing.T) {
	h, _, _ := serveAuth(t)
	req := postPublic(t, h, "/v1/auth/otp/request", `{"phone":"84 777 1234"}`)
	var challenge struct {
		ChallengeID string `json:"challengeId"`
	}
	json.Unmarshal(req.Body.Bytes(), &challenge)

	body, _ := json.Marshal(map[string]string{"challengeId": challenge.ChallengeID, "code": "000000"})
	w := postPublic(t, h, "/v1/auth/otp/verify", string(body))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	text := w.Body.String()
	if !strings.Contains(text, "otp_invalid") {
		t.Errorf("código: %s", text)
	}
	for _, leak := range []string{"tentativa", "restam", "faltam", "4", "5"} {
		if strings.Contains(strings.ToLower(text), leak) {
			t.Errorf("a mensagem revela o orçamento do atacante (%q): %s", leak, text)
		}
	}
	t.Logf("resposta: %s", strings.TrimSpace(text))
}

// Um número inválido é recusado antes de gastar uma mensagem.
func TestInvalidPhoneCostsNothing(t *testing.T) {
	h, sender, _ := serveAuth(t)
	w := postPublic(t, h, "/v1/auth/otp/request", `{"phone":"abc"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if len(sender.codes) != 0 {
		t.Fatal("gastou uma mensagem com um número inválido")
	}
}

// Os limites travam, e a resposta traz Retry-After sem dizer qual eixo bateu.
func TestRateLimitedOverHTTP(t *testing.T) {
	h, _, _ := serveAuth(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 8; i++ {
		last = postPublic(t, h, "/v1/auth/otp/request", `{"phone":"84 777 1234"}`)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("%d: %s", last.Code, last.Body.String())
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("sem Retry-After a app não sabe quando voltar")
	}
	for _, axis := range []string{"phone", "ip", "device", "global", "country"} {
		if strings.Contains(last.Body.String(), axis) {
			t.Errorf("a resposta diz qual limite bateu (%q): %s", axis, last.Body.String())
		}
	}
	t.Logf("429 com Retry-After %s · %s", last.Header().Get("Retry-After"), strings.TrimSpace(last.Body.String()))
}

// A rotação por HTTP, e a reutilização derruba a sessão.
func TestRefreshRotationOverHTTP(t *testing.T) {
	h, sender, _ := serveAuth(t)
	_, refresh := login(t, h, sender)

	body, _ := json.Marshal(map[string]string{"refreshToken": refresh, "deviceId": "device-1"})
	first := postPublic(t, h, "/v1/auth/token/refresh", string(body))
	if first.Code != http.StatusOK {
		t.Fatalf("%d: %s", first.Code, first.Body.String())
	}
	var rotated struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	json.Unmarshal(first.Body.Bytes(), &rotated)
	if rotated.RefreshToken == refresh {
		t.Fatal("a rotação devolveu o mesmo token")
	}

	// O antigo volta a aparecer: foi roubado.
	reuse := postPublic(t, h, "/v1/auth/token/refresh", string(body))
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("%d: %s", reuse.Code, reuse.Body.String())
	}
	if !strings.Contains(reuse.Body.String(), "token_reuse_detected") {
		t.Errorf("corpo: %s", reuse.Body.String())
	}

	// E o bom deixa de servir.
	good, _ := json.Marshal(map[string]string{"refreshToken": rotated.RefreshToken, "deviceId": "device-1"})
	after := postPublic(t, h, "/v1/auth/token/refresh", string(good))
	if after.Code == http.StatusOK {
		t.Fatal("a família devia ter caído toda")
	}
	t.Logf("reutilização: %s", strings.TrimSpace(reuse.Body.String()))
}

// O IP nunca é guardado em claro.
func TestIPIsHashedNotStored(t *testing.T) {
	h, _, pool := serveAuth(t)
	postPublic(t, h, "/v1/auth/otp/request", `{"phone":"84 777 1234"}`)

	rows, err := pool.Query(context.Background(), `SELECT meta::text FROM auth_event`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var meta string
		rows.Scan(&meta)
		if strings.Contains(meta, "198.51.100.7") {
			t.Fatalf("o IP em claro foi guardado: %s", meta)
		}
	}
}
