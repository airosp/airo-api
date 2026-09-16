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

/*
 * Quanto se espera por **cada** dependência.
 *
 * Três segundos e não dois: é quanto uma base serverless leva a acordar, e
 * declarar o serviço em baixo enquanto ela se levanta é criar a avaria que se
 * queria detectar.
 */
const prazoPorDependencia = 3 * time.Second

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
	/*
	 * ⚠️ **Um prazo por dependência, e não um para as três.**
	 *
	 * Isto tinha um `context` de dois segundos partilhado pelos três testes. A
	 * base é serverless e suspende-se quando ninguém a usa; o `Ping` que a
	 * acorda leva um a três segundos e gastava o orçamento todo. Os testes
	 * seguintes corriam já sem tempo e falhavam — e o que se lia era "esquema
	 * ilegível" numa base cujo esquema estava impecável.
	 *
	 * O que isso custava não era um texto errado: o EasyPanel decide pelo
	 * `/readyz` se manda tráfego, e uma suspensão de madrugada tirava a API de
	 * rotação por causa de uma base que só estava a acordar.
	 */
	prazo := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(r.Context(), prazoPorDependencia)
	}

	ctxDB, cancelDB := prazo()
	defer cancelDB()
	if err := h.DB.Ping(ctxDB); err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"status": "base de dados inacessível"})
		return
	}
	if h.Cache != nil {
		ctxCache, cancelCache := prazo()
		defer cancelCache()
		if err := h.Cache.Ping(ctxCache); err != nil {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"status": "cache inacessível", "hint": "sem Redis ninguém entra"})
			return
		}
	}
	// Servir com o esquema atrasado é o pior dos dois mundos: responde 200 e
	// falha em colunas que ainda não existem. Não-pronto, e a dizer porquê.
	if h.Schema != nil {
		ctxEsquema, cancelEsquema := prazo()
		defer cancelEsquema()
		pending, err := h.Schema.PendingCount(ctxEsquema)
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
