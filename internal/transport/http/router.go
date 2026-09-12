// Package http monta o encaminhamento.
package http

import (
	"log/slog"
	"net/http"

	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

type Deps struct {
	Log     *slog.Logger
	Version string
	DB      handlers.Pinger
}

// NewRouter devolve o handler raiz com os middlewares já aplicados.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	health := handlers.Health{Version: d.Version, DB: d.DB}
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)

	return middleware.Chain(mux,
		middleware.RequestID,
		middleware.Recover(d.Log),
		middleware.Log(d.Log),
	)
}
