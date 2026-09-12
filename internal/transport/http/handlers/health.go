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

type Health struct {
	Version string
	DB      Pinger
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": h.Version})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
