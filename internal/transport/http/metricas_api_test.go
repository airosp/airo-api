package http_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

/*
 * Os contadores: quantos pedidos, com que respostas, e quão depressa.
 *
 * ⚠️ Os registos eram estruturados e ninguém os agregava. Quando o
 * `/v1/classes` começou a devolver 500 em produção, só se soube porque alguém
 * foi ver o registo à mão.
 */
func TestAsMetricasContamOsPedidos(t *testing.T) {
	middleware.Repor()
	_, pool, userID := serve(t)
	h := airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})

	for i := 0; i < 3; i++ {
		get(t, h, "/healthz")
	}
	get(t, h, "/uma-rota-que-nao-existe")

	w := get(t, h, "/metrics")
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	corpo := w.Body.String()

	if !strings.Contains(corpo, `airo_pedidos_total{rota="/healthz",status="200"} 3`) {
		t.Errorf("sem a contagem do /healthz:\n%s", corpo)
	}
	// O histograma existe e diz quantos.
	if !strings.Contains(corpo, `airo_duracao_ms_count{rota="/healthz"} 3`) {
		t.Errorf("sem o histograma:\n%s", corpo)
	}
	if !strings.Contains(corpo, "airo_desde_segundos") {
		t.Errorf("sem o tempo de pé")
	}
}

/*
 * Um identificador na URL não cria uma métrica nova.
 *
 * ⚠️ É o erro clássico, e tem nome: cardinalidade. Mil aulas dariam mil séries
 * temporais, e o sistema que as guarda fica sem memória antes de alguém
 * reparar.
 */
func TestUmIdentificadorNaoCriaMetricaNova(t *testing.T) {
	middleware.Repor()
	h := serveCompras(t)

	// Três semanas diferentes: três caminhos, uma só métrica.
	for _, semana := range []string{"2026-09-07", "2026-09-14", "2026-09-21"} {
		get(t, h, "/v1/nutrition/shopping-list/"+semana)
	}

	corpo := get(t, h, "/metrics").Body.String()
	if strings.Contains(corpo, "2026-09-14") {
		t.Fatalf("a semana entrou na métrica:\n%s", corpo)
	}
	if !strings.Contains(corpo, `airo_pedidos_total{rota="/v1/nutrition/shopping-list/{week}",status="200"} 3`) {
		t.Errorf("sem o padrão da rota, ou sem a contagem:\n%s", corpo)
	}
}

// Um caminho que não casa com rota nenhuma fica em "outro", e não some.
func TestORestoFicaEmOutro(t *testing.T) {
	middleware.Repor()
	_, pool, userID := serve(t)
	h := airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})

	get(t, h, "/isto-nao-existe")
	corpo := get(t, h, "/metrics").Body.String()
	if !strings.Contains(corpo, `rota="outro"`) {
		t.Errorf("sem o balde do resto:\n%s", corpo)
	}
	if strings.Contains(corpo, "isto-nao-existe") {
		t.Errorf("o caminho entrou na métrica")
	}
}
