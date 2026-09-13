// Package http monta o encaminhamento.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

type Deps struct {
	Log     *slog.Logger
	Version string
	DB      handlers.Pinger
	Schema  handlers.SchemaChecker

	// Auth é opcional durante o desenvolvimento: sem ele, as rotas protegidas
	// não são registadas. Nunca ficam abertas — uma rota de escrita sem
	// autenticação é pior do que uma rota que não existe.
	Auth        middleware.TokenVerifier
	AuthAPI     *handlers.Auth
	Goals       *handlers.Goals
	Training    *handlers.Training
	Profile     *handlers.Profile
	Nutrition   *handlers.Nutrition
	Idempotency middleware.Store

	// CORSOrigins são as origens do browser autorizadas. Vazio = nenhuma, e a
	// app nativa não precisa de nenhuma.
	CORSOrigins []string
}

// NewRouter devolve o handler raiz com os middlewares já aplicados.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	health := handlers.Health{Version: d.Version, DB: d.DB, Schema: d.Schema}
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)

	// As rotas de entrada são públicas por definição: quem ainda não tem sessão
	// não pode provar que a tem. A defesa aqui são os limites, não o token.
	if d.AuthAPI != nil {
		mux.HandleFunc("POST /v1/auth/otp/request", d.AuthAPI.RequestOTP)
		mux.HandleFunc("POST /v1/auth/otp/verify", d.AuthAPI.VerifyOTP)
		mux.HandleFunc("POST /v1/auth/token/refresh", d.AuthAPI.Refresh)
		// Sair é público pela mesma razão que entrar: quem quer sair pode ter o
		// token de acesso já expirado.
		mux.HandleFunc("POST /v1/auth/logout", d.AuthAPI.Logout)
	}

	if d.Auth != nil && (d.Goals != nil || d.Training != nil || d.Profile != nil || d.Nutrition != nil) {
		store := d.Idempotency
		if store == nil {
			store = middleware.NewMemoryStore(24 * time.Hour)
		}
		// A cadeia das rotas privadas: autenticar primeiro, e só depois guardar
		// a resposta — a chave de idempotência é por utilizador, e sem saber
		// quem é não há âmbito nenhum.
		private := func(h http.HandlerFunc) http.Handler {
			return middleware.Chain(h,
				middleware.Auth(d.Auth),
				middleware.Idempotency(store, 24*time.Hour),
			)
		}
		if d.Goals != nil {
			mux.Handle("POST /v1/goals", private(d.Goals.Create))
			mux.Handle("POST /v1/goals/assess", private(d.Goals.Assess))
		}
		if d.Training != nil {
			mux.Handle("GET /v1/training/today", private(d.Training.Today))
			mux.Handle("POST /v1/training/sessions", private(d.Training.Record))
		}
		if d.Profile != nil {
			mux.Handle("GET /v1/profile", private(d.Profile.Get))
			mux.Handle("PUT /v1/profile", private(d.Profile.Put))
			// A fotografia fica fora da cadeia de idempotência: o corpo é a
			// imagem inteira, e guardar megabytes por chave para responder o
			// mesmo é pagar memória por uma repetição que ninguém faz.
			mux.Handle("POST /v1/profile/photo",
				middleware.Chain(http.HandlerFunc(d.Profile.Photo), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/profile/photo",
				middleware.Chain(http.HandlerFunc(d.Profile.DeletePhoto), middleware.Auth(d.Auth)))
		}
		if d.Nutrition != nil {
			mux.Handle("POST /v1/nutrition/meal-photo",
				middleware.Chain(http.HandlerFunc(d.Nutrition.MealPhoto), middleware.Auth(d.Auth)))
			// O diário. `PUT` com o identificador no caminho é idempotente por
			// construção, e por isso fica fora da cadeia de idempotência —
			// guardar a resposta por chave seria guardar o que já é repetível.
			mux.Handle("PUT /v1/nutrition/logs/{id}",
				middleware.Chain(http.HandlerFunc(d.Nutrition.SaveLog), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/nutrition/logs",
				middleware.Chain(http.HandlerFunc(d.Nutrition.ReadLogs), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/nutrition/logs/{id}",
				middleware.Chain(http.HandlerFunc(d.Nutrition.DeleteLog), middleware.Auth(d.Auth)))
		}
	}

	chain := []func(http.Handler) http.Handler{
		middleware.RequestID,
		middleware.Recover(d.Log),
		middleware.Log(d.Log),
	}
	if len(d.CORSOrigins) > 0 {
		// Antes do registo: um pedido prévio recusado não é um pedido da
		// aplicação, e enchia o registo com linhas que não dizem nada.
		chain = append([]func(http.Handler) http.Handler{middleware.CORS(d.CORSOrigins)}, chain...)
	}
	return middleware.Chain(mux, chain...)
}
