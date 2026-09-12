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
	Goals       *handlers.Goals
	Training    *handlers.Training
	Idempotency middleware.Store
}

// NewRouter devolve o handler raiz com os middlewares já aplicados.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	health := handlers.Health{Version: d.Version, DB: d.DB, Schema: d.Schema}
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)

	if d.Auth != nil && (d.Goals != nil || d.Training != nil) {
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
	}

	return middleware.Chain(mux,
		middleware.RequestID,
		middleware.Recover(d.Log),
		middleware.Log(d.Log),
	)
}
