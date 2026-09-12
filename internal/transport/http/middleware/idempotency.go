package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

// Idempotency guarda a resposta de uma escrita e devolve-a se o mesmo pedido
// voltar.
//
// O cliente treina offline e sincroniza depois — possivelmente duas vezes, se a
// rede voltar a meio do envio. Sem isto, reenviar cria um segundo treino: a
// sequência salta um dia e a adesão conta sessões que não existiram.
//
// A chave é do dispositivo e o âmbito é o **utilizador**: dois telemóveis podem
// gerar o mesmo uuid sem que isso queira dizer o mesmo pedido.

type stored struct {
	status  int
	body    []byte
	bodyKey string
	at      time.Time
}

// Store é onde as respostas ficam. Em produção é Redis — em memória, com duas
// instâncias, a segunda não sabe o que a primeira já respondeu.
type Store interface {
	Get(ctx context.Context, key string) (status int, body []byte, bodyKey string, ok bool)
	Put(ctx context.Context, key string, status int, body []byte, bodyKey string, ttl time.Duration)
}

// MemoryStore serve para testes e para desenvolvimento de uma instância só.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]stored
	ttl  time.Duration
}

func NewMemoryStore(ttl time.Duration) *MemoryStore {
	return &MemoryStore{data: map[string]stored{}, ttl: ttl}
}

func (m *MemoryStore) Get(_ context.Context, key string) (int, []byte, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok || time.Since(e.at) > m.ttl {
		return 0, nil, "", false
	}
	return e.status, e.body, e.bodyKey, true
}

func (m *MemoryStore) Put(_ context.Context, key string, status int, body []byte, bodyKey string, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = stored{status: status, body: body, bodyKey: bodyKey, at: time.Now()}
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// Idempotency aplica-se a POST, PUT e PATCH. Um GET já é idempotente.
func Idempotency(store Store, ttl time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" || (r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch) {
				next.ServeHTTP(w, r)
				return
			}
			userID, _ := UserID(r.Context())

			body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
			if err != nil {
				apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido ilegível.", "")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			sum := sha256.Sum256(body)
			bodyKey := hex.EncodeToString(sum[:])
			cacheKey := userID + "|" + r.Method + "|" + r.URL.Path + "|" + key

			if status, cached, storedBody, ok := store.Get(r.Context(), cacheKey); ok {
				// A mesma chave com corpo diferente é um erro do cliente, não um
				// reenvio: devolver a resposta antiga esconderia a confusão.
				if storedBody != bodyKey {
					apierr.Write(w, apierr.ValidationFailed,
						"Esta chave de idempotência já foi usada com outro conteúdo.", "Idempotency-Key")
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Idempotent-Replay", "true")
				w.WriteHeader(status)
				_, _ = w.Write(cached)
				return
			}

			cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(cw, r)

			// Só se guarda o que correu bem. Repetir um pedido que falhou por
			// erro do servidor tem de poder voltar a tentar.
			if cw.status >= 200 && cw.status < 300 {
				store.Put(r.Context(), cacheKey, cw.status, cw.body.Bytes(), bodyKey, ttl)
			}
		})
	}
}
