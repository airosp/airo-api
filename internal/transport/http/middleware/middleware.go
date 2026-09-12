// Package middleware tem o que corre antes de cada handler.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

// RequestID marca cada pedido. Sem isto, investigar um erro relatado por alguém
// é procurar uma linha entre milhares sem saber qual.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Recover impede que um pânico num handler leve o servidor inteiro. Devolve 500
// e regista o rasto — que nunca vai para a resposta.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Error("pânico no handler",
						"error", err,
						"path", r.URL.Path,
						"request_id", RequestIDFrom(r.Context()),
						"stack", string(debug.Stack()))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"Erro interno."}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Log regista cada pedido depois de servido, com a duração.
func Log(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			ctx := apierr.WithCause(r.Context())
			r = r.WithContext(ctx)
			next.ServeHTTP(sw, r)

			args := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(ctx),
			}
			// Um 500 sem motivo é um número. Quem o guardou di-lo aqui.
			if cause := apierr.CauseFrom(ctx); cause != nil {
				log.Error("pedido falhou", append(args, "error", cause)...)
				return
			}
			log.Info("pedido", args...)
		})
	}
}

// Chain aplica os middlewares pela ordem em que são dados: o primeiro é o mais
// exterior, e por isso é o primeiro a ver o pedido.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
