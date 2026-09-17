// Package sms entrega o código por mensagem, quando o WhatsApp não serve.
//
// ⚠️ Trinta segundos sem entrega e não havia plano B: quem não tem WhatsApp
// activo ficava à porta da app, e do nosso lado ninguém sabia quantos eram.
// Em Moçambique isso não é um caso de canto — é muita gente.
//
// Três coisas a reter:
//
//  1. O `202` do fornecedor diz **aceite**, não **entregue**. É o mesmo erro do
//     WhatsApp e engana da mesma maneira.
//  2. O número vai em E.164, com `+`. Ao contrário do WhatsApp, aqui o `+` é
//     obrigatório: sem ele o fornecedor trata-o como número curto local.
//  3. Uma mensagem custa dinheiro a cada envio. O limite de pedidos é o que
//     separa uma conta de uma fatura.
package sms

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	// Provider escolhe o feitio do pedido. Hoje só "twilio"; outro fornecedor
	// é um ficheiro novo aqui dentro, não um `if` espalhado pelo serviço.
	Provider string
	// AccountSID e AuthToken são as credenciais. From é o remetente — um
	// número comprado ou um nome de remetente aprovado.
	AccountSID string
	AuthToken  string
	From       string
	// BaseURL existe para os testes apontarem para um servidor local.
	BaseURL string
	HTTP    *http.Client
	Log     *slog.Logger
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	if cfg.HTTP == nil {
		// Um prazo curto de propósito: quem está à espera do código está a
		// olhar para o ecrã, e um fornecedor lento não pode segurar o pedido.
		cfg.HTTP = &http.Client{Timeout: 8 * time.Second}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.twilio.com"
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Client{cfg: cfg}
}

// Configured diz se há com que enviar. Sem isto, o serviço tentaria e falharia
// a cada pedido — e cada falha custa uma tentativa a quem quer entrar.
func (c *Client) Configured() bool {
	return c != nil && c.cfg.AccountSID != "" && c.cfg.AuthToken != "" && c.cfg.From != ""
}

/*
 * ErrNaoEntregavel: este número não recebe mensagens.
 *
 * Distinto de "falhou agora" pela mesma razão que no WhatsApp: a primeira não
 * melhora com uma segunda tentativa, e quem pediu não fez nada de errado.
 */
var ErrNaoEntregavel = errors.New("número não recebe mensagens")

// Undeliverable faz este erro encaixar na interface que o serviço já conhece.
type naoEntregavel struct{ error }

func (naoEntregavel) Undeliverable() bool { return true }

// Unwrap, para o `errors.Is` chegar ao `ErrNaoEntregavel` lá dentro. Sem isto,
// o erro dizia a coisa certa em texto e mentia a quem o interrogasse.
func (e naoEntregavel) Unwrap() error { return e.error }

/*
 * Send entrega o código e devolve o identificador da mensagem.
 *
 * O texto é curto e sem ligações: uma mensagem com um endereço lá dentro é a
 * forma de todas as burlas por SMS, e ensinar as pessoas a tocar em ligações
 * que chegam por mensagem é ensiná-las a serem enganadas.
 */
func (c *Client) Send(ctx context.Context, telefone, codigo string) (string, error) {
	if !c.Configured() {
		return "", errors.New("sms: sem credenciais")
	}

	corpo := url.Values{}
	corpo.Set("To", telefone)
	corpo.Set("From", c.cfg.From)
	corpo.Set("Body", fmt.Sprintf("%s é o teu código Airo. Não o partilhes com ninguém.", codigo))

	destino := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json",
		strings.TrimRight(c.cfg.BaseURL, "/"), url.PathEscape(c.cfg.AccountSID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destino,
		bytes.NewBufferString(corpo.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte(c.cfg.AccountSID+":"+c.cfg.AuthToken)))

	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("sms: %w", err)
	}
	defer resp.Body.Close()

	bruto, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out struct {
			SID string `json:"sid"`
		}
		_ = json.Unmarshal(bruto, &out)
		return out.SID, nil
	}

	var erro struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(bruto, &erro)

	/*
	 * Os códigos que querem dizer "este número não serve".
	 *
	 * 21211 número inválido, 21408 região não permitida, 21610 destinatário
	 * que pediu para não receber, 21614 número sem SMS. Nenhum melhora com uma
	 * segunda tentativa, e todos custam uma tentativa a quem está a tentar
	 * entrar — por isso distinguem-se.
	 */
	switch erro.Code {
	case 21211, 21408, 21610, 21614:
		return "", naoEntregavel{fmt.Errorf("%w: %d %s", ErrNaoEntregavel, erro.Code, erro.Message)}
	}
	return "", fmt.Errorf("sms: %d: %d %s", resp.StatusCode, erro.Code, erro.Message)
}
