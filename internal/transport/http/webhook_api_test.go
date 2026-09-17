package http_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

const segredoDeEnsaio = "app-secret-de-ensaio"

func serveWebhook(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	_, pool, _ := serve(t)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		WhatsApp: &handlers.WhatsAppWebhook{
			Store:       repo.NewAuthRepo(repo.NewTxManager(pool)),
			Secret:      segredoDeEnsaio,
			VerifyToken: "palavra-passe-da-subscricao",
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), pool
}

/** Assina como a Meta assina: HMAC-SHA256 do corpo, em hexadecimal. */
func assinar(corpo, segredo string) string {
	mac := hmac.New(sha256.New, []byte(segredo))
	mac.Write([]byte(corpo))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func webhook(t *testing.T, h http.Handler, corpo, assinatura string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/whatsapp", strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	if assinatura != "" {
		r.Header.Set("X-Hub-Signature-256", assinatura)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const eventoEntregue = `{"entry":[{"changes":[{"value":{"statuses":[
  {"id":"wamid.ABC","status":"delivered","recipient_id":"258841234567"}]}}]}]}`

/*
 * Sem assinatura não entra.
 *
 * ⚠️ Hoje um evento forjado só apagaria uma dúvida nossa. No dia em que a
 * entrega decidir se se tenta por SMS, decide por quem entra.
 */
func TestWebhookSemAssinaturaERecusado(t *testing.T) {
	h, _ := serveWebhook(t)

	if w := webhook(t, h, eventoEntregue, ""); w.Code != http.StatusForbidden {
		t.Errorf("sem cabeçalho: %d", w.Code)
	}
	if w := webhook(t, h, eventoEntregue, "sha256=abcdef"); w.Code != http.StatusForbidden {
		t.Errorf("assinatura inventada: %d", w.Code)
	}
	if w := webhook(t, h, eventoEntregue, assinar(eventoEntregue, "outro-segredo")); w.Code != http.StatusForbidden {
		t.Errorf("assinada com o segredo errado: %d", w.Code)
	}
	if w := webhook(t, h, eventoEntregue, "abcdef"); w.Code != http.StatusForbidden {
		t.Errorf("sem o prefixo sha256=: %d", w.Code)
	}
}

/*
 * O corpo mexido invalida a assinatura.
 *
 * A assinatura é sobre os bytes recebidos. Sem isso, bastava assinar um corpo
 * inofensivo e mandar outro.
 */
func TestWebhookComCorpoMexidoERecusado(t *testing.T) {
	h, _ := serveWebhook(t)
	boa := assinar(eventoEntregue, segredoDeEnsaio)
	mexido := strings.Replace(eventoEntregue, "wamid.ABC", "wamid.XYZ", 1)

	if w := webhook(t, h, mexido, boa); w.Code != http.StatusForbidden {
		t.Fatalf("aceitou um corpo diferente do assinado: %d", w.Code)
	}
}

/*
 * Assinado e conhecido: a entrega fica gravada.
 *
 * O `200` do envio diz **aceite**; é isto que diz entregue.
 */
func TestWebhookGravaAEntrega(t *testing.T) {
	h, pool := serveWebhook(t)
	criarDesafio(t, pool, "wamid.ABC", "+258841234567")

	w := webhook(t, h, eventoEntregue, assinar(eventoEntregue, segredoDeEnsaio))
	if w.Code != http.StatusOK {
		t.Fatalf("%d — %s", w.Code, w.Body.String())
	}
	if got := estadoDaEntrega(t, pool, "wamid.ABC"); got != "delivered" {
		t.Errorf("ficou %q", got)
	}
}

/*
 * Os estados só avançam.
 *
 * Os eventos da Meta chegam fora de ordem com frequência, e um `sent` atrasado
 * não pode desfazer um `read` que já lá estava.
 */
func TestOsEstadosSoAvancam(t *testing.T) {
	h, pool := serveWebhook(t)
	criarDesafio(t, pool, "wamid.ABC", "+258841234567")

	for _, estado := range []string{"sent", "read", "delivered", "sent"} {
		corpo := strings.Replace(eventoEntregue, "delivered", estado, 1)
		if w := webhook(t, h, corpo, assinar(corpo, segredoDeEnsaio)); w.Code != http.StatusOK {
			t.Fatalf("%s: %d", estado, w.Code)
		}
	}
	if got := estadoDaEntrega(t, pool, "wamid.ABC"); got != "read" {
		t.Errorf("um evento atrasado recuou o estado para %q", got)
	}
}

/*
 * Um evento que não é nosso responde 200 na mesma.
 *
 * A Meta repete o que não recebe um 200, e um evento que não nos diz respeito
 * repetido para sempre é ruído que esconde os que dizem.
 */
func TestEventoDesconhecidoNaoERepetido(t *testing.T) {
	h, _ := serveWebhook(t)
	if w := webhook(t, h, eventoEntregue, assinar(eventoEntregue, segredoDeEnsaio)); w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}

	// E um corpo assinado que não se consegue ler também não se pede outra vez.
	lixo := `{"entry":"isto não é uma lista"}`
	if w := webhook(t, h, lixo, assinar(lixo, segredoDeEnsaio)); w.Code != http.StatusOK {
		t.Errorf("corpo ilegível: %d", w.Code)
	}
}

// O aperto de mão da subscrição: devolve o desafio, e só a quem sabe a palavra.
func TestApertoDeMaoDaSubscricao(t *testing.T) {
	h, _ := serveWebhook(t)

	w := get(t, h, "/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=palavra-passe-da-subscricao&hub.challenge=12345")
	if w.Code != http.StatusOK || w.Body.String() != "12345" {
		t.Fatalf("%d — %q", w.Code, w.Body.String())
	}

	w = get(t, h, "/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=errada&hub.challenge=12345")
	if w.Code != http.StatusForbidden {
		t.Errorf("palavra errada: %d", w.Code)
	}
	w = get(t, h, "/v1/webhooks/whatsapp?hub.mode=unsubscribe&hub.verify_token=palavra-passe-da-subscricao")
	if w.Code != http.StatusForbidden {
		t.Errorf("modo desconhecido: %d", w.Code)
	}
}

/** Um desafio com um identificador de mensagem, para o webhook encontrar. */
func criarDesafio(t *testing.T, pool *pgxpool.Pool, messageID, telefone string) {
	t.Helper()
	agora := time.Now().UTC()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO otp_challenge
		   (phone_e164, code_hash, channel, created_at, expires_at, max_attempts, provider_message_id)
		 VALUES ($1, '\x00', 'whatsapp', $2, $3, 5, $4)`,
		telefone, agora, agora.Add(5*time.Minute), messageID); err != nil {
		t.Fatal(err)
	}
}

func estadoDaEntrega(t *testing.T, pool *pgxpool.Pool, messageID string) string {
	t.Helper()
	var estado *string
	if err := pool.QueryRow(context.Background(),
		`SELECT delivery_status FROM otp_challenge WHERE provider_message_id = $1`,
		messageID).Scan(&estado); err != nil {
		t.Fatal(err)
	}
	if estado == nil {
		return "—"
	}
	return *estado
}
