package http_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/redis/go-redis/v9"
)

/*
 * A sessão da web num cookie que o script não lê.
 *
 * ⚠️ É autenticação: o que aqui falhar em produção tranca as pessoas fora da
 * app. Por isso está provado dos dois lados — que o cookie se põe, que serve
 * para renovar sem o corpo, que sai na saída, e que **não se põe** a quem não
 * manda o cabeçalho que o CSRF exige.
 */
func serveAuthComCookie(t *testing.T, ligado bool) (http.Handler, *codeCapture) {
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
	cfg := service.DefaultAuthConfig(pepper)

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		AuthAPI: &handlers.Auth{
			Service: service.NewAuthService(
				repo.NewAuthRepo(repo.NewTxManager(pool)), auth.NewRedisLimiter(rdb),
				sender, cfg, clk),
			Tokens:           tokens,
			CookieDeSessao:   ligado,
			DuracaoDoRefresh: cfg.RefreshTTL,
		},
		Auth:        tokens,
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), sender
}

/** Um pedido público, com ou sem o cabeçalho que prova ser um cliente web. */
func postWeb(t *testing.T, h http.Handler, path, body string, comCabecalho bool, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	if comCabecalho {
		r.Header.Set(handlers.CabecalhoDeCliente, "web")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

/** Entra e devolve a resposta, para se poder olhar aos cookies. */
func entrarNaWeb(t *testing.T, h http.Handler, sender *codeCapture, comCabecalho bool) *httptest.ResponseRecorder {
	t.Helper()
	pedido := postWeb(t, h, "/v1/auth/otp/request",
		`{"phone":"`+phone+`","channel":"whatsapp","deviceId":"device-web"}`, comCabecalho, nil)
	if pedido.Code != http.StatusAccepted {
		t.Fatalf("pedir código: %d — %s", pedido.Code, pedido.Body.String())
	}
	var desafio struct {
		ChallengeID string `json:"challengeId"`
	}
	json.Unmarshal(pedido.Body.Bytes(), &desafio)

	corpo, _ := json.Marshal(map[string]string{
		"challengeId": desafio.ChallengeID, "code": sender.codes[phone],
		"deviceId": "device-web", "platform": "web",
	})
	w := postWeb(t, h, "/v1/auth/otp/verify", string(corpo), comCabecalho, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("verificar: %d — %s", w.Code, w.Body.String())
	}
	return w
}

func cookieDaSessao(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == handlers.NomeDoCookieDeSessao {
			return c
		}
	}
	return nil
}

func refreshDoCorpo(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var s struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("resposta ilegível: %v", err)
	}
	return s.RefreshToken
}

/*
 * Na web o refresh vai no cookie e **sai do corpo**.
 *
 * Deixá-lo nos dois sítios não ganhava nada: o ponto do cookie é o script da
 * página não lhe chegar, e um refresh que volta no JSON está ao alcance de
 * qualquer script que leia a resposta.
 */
func TestNaWebORefreshVaiNoCookieENaoNoCorpo(t *testing.T) {
	h, sender := serveAuthComCookie(t, true)
	w := entrarNaWeb(t, h, sender, true)

	c := cookieDaSessao(w)
	if c == nil {
		t.Fatal("entrou sem cookie de sessão")
	}
	if c.Value == "" {
		t.Error("cookie vazio")
	}
	if !c.HttpOnly {
		t.Error("o cookie não é httpOnly — qualquer script da página o lê, que é o que isto existe para impedir")
	}
	if !c.Secure {
		t.Error("o cookie não é Secure")
	}
	// `None` e não `Strict`: a app web e a API estão em sites diferentes, e um
	// cookie `Strict` nunca sairia do browser.
	if c.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite é %v — com sites diferentes tem de ser None", c.SameSite)
	}
	if c.Path != "/v1/auth" {
		t.Errorf("o cookie viaja em %q — devia ficar preso às rotas de autenticação", c.Path)
	}
	if r := refreshDoCorpo(t, w); r != "" {
		t.Error("o refresh voltou no corpo ao lado do cookie — está ao alcance de qualquer script")
	}
}

/*
 * Sem o cabeçalho não há cookie: é um cliente nativo, e recebe como sempre.
 *
 * É também a defesa de CSRF: um formulário de outro site não consegue pôr
 * cabeçalhos, e sem o cabeçalho o servidor nem entra neste caminho.
 */
func TestSemOCabecalhoDeClienteNaoHaCookie(t *testing.T) {
	h, sender := serveAuthComCookie(t, true)
	w := entrarNaWeb(t, h, sender, false)

	if c := cookieDaSessao(w); c != nil {
		t.Error("pôs cookie a quem não o pediu")
	}
	if refreshDoCorpo(t, w) == "" {
		t.Error("um cliente nativo ficou sem refresh nenhum")
	}
}

// Desligado, nada muda: é o estado em que isto vai para produção.
func TestDesligadoAWebContinuaComORefreshNoCorpo(t *testing.T) {
	h, sender := serveAuthComCookie(t, false)
	w := entrarNaWeb(t, h, sender, true)

	if c := cookieDaSessao(w); c != nil {
		t.Error("pôs cookie com a funcionalidade desligada")
	}
	if refreshDoCorpo(t, w) == "" {
		t.Error("ficou sem refresh nos dois sítios")
	}
}

/*
 * O cookie renova a sessão sem o corpo levar token nenhum.
 *
 * É a razão de tudo isto: recarregar a página deixa de ser sair.
 */
func TestOCookieRenovaASessaoSemTokenNoCorpo(t *testing.T) {
	h, sender := serveAuthComCookie(t, true)
	entrada := entrarNaWeb(t, h, sender, true)
	c := cookieDaSessao(entrada)
	if c == nil {
		t.Fatal("entrou sem cookie")
	}

	// Corpo sem refresh: é o que o browser manda quando o token vive no cookie.
	w := postWeb(t, h, "/v1/auth/token/refresh", `{"deviceId":"device-web"}`, true, []*http.Cookie{c})
	if w.Code != http.StatusOK {
		t.Fatalf("renovar pelo cookie: %d — %s", w.Code, w.Body.String())
	}
	var s struct {
		AccessToken string `json:"accessToken"`
	}
	json.Unmarshal(w.Body.Bytes(), &s)
	if s.AccessToken == "" {
		t.Error("renovou sem devolver um token de acesso")
	}

	// A rotação roda o cookie: o anterior deixou de valer, e um cookie a
	// apontar para ele era uma sessão que morria na renovação seguinte.
	novo := cookieDaSessao(w)
	if novo == nil {
		t.Fatal("renovou sem rodar o cookie")
	}
	if novo.Value == c.Value {
		t.Error("o cookie não mudou — a rotação do refresh não chegou lá")
	}
}

/*
 * Sair leva o cookie com a sessão.
 *
 * Sem isto ficava no browser a apontar para um token revogado, e o ecrã
 * seguinte tentava renovar com ele — uma sessão fantasma que só falha ao usar.
 */
func TestSairApagaOCookie(t *testing.T) {
	h, sender := serveAuthComCookie(t, true)
	entrada := entrarNaWeb(t, h, sender, true)
	c := cookieDaSessao(entrada)

	w := postWeb(t, h, "/v1/auth/logout", `{}`, true, []*http.Cookie{c})
	if w.Code != http.StatusNoContent {
		t.Fatalf("sair: %d — %s", w.Code, w.Body.String())
	}
	apagado := cookieDaSessao(w)
	if apagado == nil {
		t.Fatal("saiu sem apagar o cookie")
	}
	if apagado.MaxAge >= 0 || apagado.Value != "" {
		t.Errorf("o cookie não foi apagado: valor=%q maxAge=%d", apagado.Value, apagado.MaxAge)
	}

	// E a sessão morreu mesmo: o cookie antigo já não renova.
	if w := postWeb(t, h, "/v1/auth/token/refresh", `{"deviceId":"device-web"}`, true, []*http.Cookie{c}); w.Code == http.StatusOK {
		t.Error("o cookie de uma sessão terminada ainda renova")
	}
}

/*
 * O corpo ganha ao cookie.
 *
 * Um cliente que manda o refresh explicitamente está a dizer qual quer, e um
 * cookie esquecido de outra sessão não lhe deve passar à frente.
 */
func TestOCorpoGanhaAoCookie(t *testing.T) {
	h, sender := serveAuthComCookie(t, true)

	// Uma sessão com cookie, e outra sem — duas sessões do mesmo número.
	comCookie := entrarNaWeb(t, h, sender, true)
	cookie := cookieDaSessao(comCookie)
	semCookie := entrarNaWeb(t, h, sender, false)
	doCorpo := refreshDoCorpo(t, semCookie)
	if doCorpo == "" {
		t.Fatal("a segunda sessão não devolveu refresh")
	}

	// Com os dois presentes, é o do corpo que renova.
	corpo := `{"refreshToken":"` + doCorpo + `","deviceId":"device-web"}`
	if w := postWeb(t, h, "/v1/auth/token/refresh", corpo, true, []*http.Cookie{cookie}); w.Code != http.StatusOK {
		t.Fatalf("renovar pelo corpo: %d — %s", w.Code, w.Body.String())
	}
	// E o do cookie continua a valer: não foi ele que rodou.
	if w := postWeb(t, h, "/v1/auth/token/refresh", `{"deviceId":"device-web"}`, true, []*http.Cookie{cookie}); w.Code != http.StatusOK {
		t.Errorf("o cookie deixou de valer depois de uma renovação que não era a dele: %d", w.Code)
	}
}
