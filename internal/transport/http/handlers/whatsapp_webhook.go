package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// DeliveryStore grava o que o canal diz sobre a entrega de cada mensagem.
type DeliveryStore interface {
	MarkDelivery(ctx context.Context, messageID, status string) (phone string, ok bool, err error)
	AppendAuthEvent(ctx context.Context, kind string, userID, phone, deviceID *string, meta map[string]any) error
}

/*
 * WhatsAppWebhook recebe as confirmações de entrega da Meta.
 *
 * ⚠️ **O `200` do envio diz aceite, não entregue.** A entrega chega por aqui, e
 * até agora não chegava a lado nenhum: não havia rota. Quem não recebesse a
 * mensagem ficava à porta e do nosso lado ninguém sabia — nem quantos, nem
 * quais, nem se o problema era o template, o número ou a Meta.
 *
 * E sem verificar a assinatura, qualquer um podia dizer ao servidor que uma
 * mensagem tinha sido entregue. Hoje isso só apagaria uma dúvida nossa; no dia
 * em que a entrega decidir se se tenta por SMS, decide por quem entrar.
 */
type WhatsAppWebhook struct {
	Store DeliveryStore
	/*
	 * Secret é o **app secret** da aplicação na Meta — o mesmo com que ela
	 * assina o corpo. Vazio desliga a rota por inteiro: uma rota de webhook
	 * aberta é pior do que rota nenhuma.
	 */
	Secret string
	/*
	 * VerifyToken é o que a Meta devolve no `GET` de subscrição, à escolha de
	 * quem configura. Vazio usa o `Secret`, que é o que já é preciso guardar.
	 */
	VerifyToken string
}

/** Um tecto: o corpo destes eventos são alguns kilobytes. */
const maximoBytesDoWebhook = 512 * 1024

/*
 * Verify responde ao aperto de mão da subscrição.
 *
 * A Meta chama `GET` com `hub.challenge` e devolve-se o valor em texto simples.
 * O `hub.verify_token` é o que impede que outra pessoa aponte a nossa rota para
 * a aplicação dela.
 */
func (h WhatsAppWebhook) Verify(w http.ResponseWriter, r *http.Request) {
	if h.Secret == "" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	esperado := h.VerifyToken
	if esperado == "" {
		esperado = h.Secret
	}
	// Comparação de tempo constante: comparar com `==` diz, pelo tempo que
	// demora, quantos caracteres do princípio estão certos.
	if q.Get("hub.mode") != "subscribe" ||
		!hmac.Equal([]byte(q.Get("hub.verify_token")), []byte(esperado)) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(q.Get("hub.challenge")))
}

/*
 * Receive recebe os eventos de estado.
 *
 * Responde `200` a tudo o que esteja assinado, incluindo o que não reconhece: a
 * Meta repete o que não recebe um `200`, e um evento que não nos diz respeito
 * repetido para sempre é ruído que esconde os que dizem.
 */
func (h WhatsAppWebhook) Receive(w http.ResponseWriter, r *http.Request) {
	if h.Secret == "" || h.Store == nil {
		http.NotFound(w, r)
		return
	}

	corpo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maximoBytesDoWebhook))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// A assinatura é sobre os **bytes recebidos**, e não sobre o que se
	// descodificou: voltar a serializar dá outros bytes e outra assinatura.
	if !assinaturaValida(r.Header.Get("X-Hub-Signature-256"), corpo, h.Secret) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var evento struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Statuses []struct {
						ID        string `json:"id"`
						Status    string `json:"status"`
						Recipient string `json:"recipient_id"`
						Errors    []struct {
							Code    int    `json:"code"`
							Title   string `json:"title"`
							Message string `json:"message"`
						} `json:"errors"`
					} `json:"statuses"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(corpo, &evento); err != nil {
		// Assinado mas ilegível: é nosso problema de leitura, não da Meta.
		// Responde-se 200 na mesma para não pedir repetição do que não vamos
		// conseguir ler à segunda.
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, entrada := range evento.Entry {
		for _, mudanca := range entrada.Changes {
			for _, estado := range mudanca.Value.Statuses {
				h.registar(r.Context(), estado.ID, estado.Status, primeiroErro(estado.Errors))
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h WhatsAppWebhook) registar(ctx context.Context, id, estado string, motivo map[string]any) {
	if id == "" || estado == "" {
		return
	}
	if estado == "failed" {
		// Uma falha não avança o estado — regista-se como acontecimento, que é
		// o que interessa a quem está a perceber porque é que alguém não entrou.
		_ = h.Store.AppendAuthEvent(ctx, "otp_delivery_failed", nil, nil, nil, motivo)
		return
	}

	phone, ok, err := h.Store.MarkDelivery(ctx, id, estado)
	if err != nil || !ok {
		// Identificador que não é de nenhum desafio nosso, ou um estado que não
		// avança. Nos dois casos não há nada a fazer — e nada a repetir.
		return
	}
	_ = h.Store.AppendAuthEvent(ctx, "otp_"+estado, nil, &phone, nil, nil)
}

/*
 * assinaturaValida confere o `X-Hub-Signature-256`.
 *
 * HMAC-SHA256 do corpo com o app secret, em hexadecimal, prefixado por
 * `sha256=`. A comparação é de tempo constante: com `==`, o tempo que a
 * comparação demora diz quantos caracteres do princípio estão certos, e isso
 * chega para descobrir a assinatura inteira byte a byte.
 */
func assinaturaValida(cabecalho string, corpo []byte, segredo string) bool {
	if cabecalho == "" {
		return false
	}
	valor, ok := strings.CutPrefix(cabecalho, "sha256=")
	if !ok {
		return false
	}
	recebida, err := hex.DecodeString(valor)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(segredo))
	mac.Write(corpo)
	return hmac.Equal(recebida, mac.Sum(nil))
}

func primeiroErro(errs []struct {
	Code    int    `json:"code"`
	Title   string `json:"title"`
	Message string `json:"message"`
}) map[string]any {
	if len(errs) == 0 {
		return nil
	}
	return map[string]any{
		"code": errs[0].Code, "title": errs[0].Title, "message": errs[0].Message,
	}
}
