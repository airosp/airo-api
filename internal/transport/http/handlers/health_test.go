package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// baseLenta acorda devagar, como uma base serverless suspensa.
type baseLenta struct{ demora time.Duration }

func (b baseLenta) Ping(ctx context.Context) error {
	select {
	case <-time.After(b.demora):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// esquemaEmDia responde depressa — desde que ainda tenha tempo para responder.
type esquemaEmDia struct{}

func (esquemaEmDia) PendingCount(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return 0, nil
}

/*
 * Uma base que demora a acordar não faz o esquema parecer ilegível.
 *
 * ⚠️ Os três testes partilhavam um prazo de dois segundos. O `Ping` que acorda
 * a base gastava-o quase todo e o do esquema corria já sem tempo — o `/readyz`
 * respondia "esquema ilegível" sobre uma base cujo esquema estava impecável, e
 * o encaminhador tirava a API de rotação.
 */
func TestUmaBaseSonolentaNaoDerrubaOResto(t *testing.T) {
	h := Health{
		Version: "test",
		DB:      baseLenta{demora: 1800 * time.Millisecond},
		Schema:  esquemaEmDia{},
	}

	w := httptest.NewRecorder()
	h.Ready(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("deu %d — %s", w.Code, w.Body.String())
	}
	var corpo map[string]string
	json.Unmarshal(w.Body.Bytes(), &corpo)
	if corpo["status"] != "ok" {
		t.Errorf("estado %q, esperava ok", corpo["status"])
	}
}

// Uma base mesmo em baixo continua a dar não-pronto — é para isso que serve.
func TestBaseEmBaixoContinuaANaoEstarPronta(t *testing.T) {
	h := Health{
		Version: "test",
		// Mais do que qualquer prazo razoável: isto não é lentidão, é ausência.
		DB:     baseLenta{demora: 30 * time.Second},
		Schema: esquemaEmDia{},
	}

	w := httptest.NewRecorder()
	h.Ready(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("deu %d, esperava 503", w.Code)
	}
	var corpo map[string]string
	json.Unmarshal(w.Body.Bytes(), &corpo)
	if corpo["status"] != "base de dados inacessível" {
		t.Errorf("estado %q — devia apontar à base, não ao esquema", corpo["status"])
	}
}
