package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger é o que a saúde precisa de saber sobre a base de dados. Interface e
// não o pool concreto: o handler não tem de saber que existe pgx.
type Pinger interface {
	Ping(ctx context.Context) error
}

// SchemaChecker diz quantas migrações faltam. Interface para o handler não
// precisar de saber que existe um executor de migrações.
type SchemaChecker interface {
	PendingCount(ctx context.Context) (int, error)
}

type Health struct {
	Version string
	DB      Pinger
	Schema  SchemaChecker
	// Cache é o Redis. Opcional no arranque — em desenvolvimento a API sobe
	// sem ele — mas quando existe tem de responder: sem Redis não há limites
	// de entrada, e sem limites de entrada as rotas de autenticação nem sequer
	// são registadas. Dizer-se pronto nesse estado é dizer que se serve quando
	// **ninguém consegue entrar**.
	Cache Pinger
}

// Live diz que o processo está de pé. Não toca em dependências — um balanceador
// que reinicia a API porque a base de dados piscou torna uma falha em duas.
func (h Health) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": h.Version})
}

// Ready diz que o processo pode servir. Aqui sim, toca nas dependências.
func (h Health) Ready(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"status": "sem base de dados"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.DB.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"status": "base de dados inacessível"})
		return
	}
	if h.Cache != nil {
		if err := h.Cache.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"status": "cache inacessível", "hint": "sem Redis ninguém entra"})
			return
		}
	}
	// Servir com o esquema atrasado é o pior dos dois mundos: responde 200 e
	// falha em colunas que ainda não existem. Não-pronto, e a dizer porquê.
	if h.Schema != nil {
		pending, err := h.Schema.PendingCount(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"status": "esquema ilegível"})
			return
		}
		if pending > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "esquema desactualizado",
				"pending": pending,
				"hint":    "airo-api migrate up",
				"version": h.Version,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": h.Version})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
