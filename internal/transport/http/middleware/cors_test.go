package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

func corsHandler() http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	return middleware.CORS([]string{"http://localhost:8081", "https://app.airo.co.mz/"})(ok)
}

func TestPedidoPrevioAutorizadoNaoChegaAoHandler(t *testing.T) {
	r := httptest.NewRequest(http.MethodOptions, "/v1/auth/otp/request", nil)
	r.Header.Set("Origin", "http://localhost:8081")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	corsHandler().ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("pedido prévio devia terminar em 204; deu %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:8081" {
		t.Fatalf("origem autorizada = %q", got)
	}
	// Sem `Authorization` na lista, o browser recusa todos os pedidos com
	// sessão — e a app fica a funcionar só enquanto ninguém entra.
	if h := w.Header().Get("Access-Control-Allow-Headers"); !contains(h, "Authorization") ||
		!contains(h, "Idempotency-Key") {
		t.Fatalf("cabeçalhos autorizados = %q", h)
	}
	// `Retry-After` não está na lista segura do browser: sem o expor, o ecrã
	// diz "tenta daqui a pouco" sem saber quanto tempo.
	if e := w.Header().Get("Access-Control-Expose-Headers"); !contains(e, "Retry-After") {
		t.Fatalf("cabeçalhos expostos = %q", e)
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Fatal("falta Vary: Origin — uma cache serviria a resposta de uma origem a outra")
	}
}

func TestOrigemDesconhecidaNaoRecebeAutorizacao(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/goals", nil)
	r.Header.Set("Origin", "https://nao-e-a-airo.example")
	w := httptest.NewRecorder()
	corsHandler().ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("origem desconhecida recebeu autorização: %q", got)
	}
	// E o pedido segue: quem não é browser não é afectado por isto.
	if w.Code != http.StatusTeapot {
		t.Fatalf("o pedido devia chegar ao handler; deu %d", w.Code)
	}
}

// A barra final não pode decidir se uma origem é conhecida.
func TestBarraFinalNaoConta(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("Origin", "https://app.airo.co.mz")
	w := httptest.NewRecorder()
	corsHandler().ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "https://app.airo.co.mz" {
		t.Fatal("a origem configurada com barra devia bater certo sem ela")
	}
}

// Sem origem — a app nativa, o curl, um teste — nada muda.
func TestSemOrigemNadaMuda(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	corsHandler().ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("sem Origin não se devolve autorização nenhuma")
	}
	if w.Code != http.StatusTeapot {
		t.Fatalf("o pedido devia passar; deu %d", w.Code)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
