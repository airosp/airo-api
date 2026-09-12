// Package whatsapp entrega o código de autenticação pela Cloud API da Meta.
//
// Contrato em docs/backend/08-autenticacao.md §12. Três coisas a reter, porque
// cada uma já foi assumida ao contrário:
//
//  1. O `200` da Meta diz **aceite**, não **entregue**. A entrega chega depois,
//     por webhook. Quem tratar o sucesso como entrega fica a dever uma
//     mensagem a alguém.
//  2. O template tem de ser de categoria AUTHENTICATION e estar aprovado.
//     Enviar por um template de marketing é recusado com `132001`.
//  3. O número vai **sem `+`**. Com `+` a Meta aceita e não entrega.
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const defaultGraphVersion = "v21.0"

type Config struct {
	// PhoneNumberID é o número **remetente**, não o destinatário.
	PhoneNumberID string
	Token         string
	// Template aprovado, categoria AUTHENTICATION.
	Template string
	// Language é o código de língua do template. Um template aprovado em
	// `pt_PT` não aceita `pt_BR`: são templates diferentes para a Meta.
	Language string
	// GraphVersion fixa a versão da API. Não seguir a mais recente é
	// deliberado: uma mudança de versão tem de ser uma decisão, não uma
	// surpresa numa terça-feira.
	GraphVersion string
	// BaseURL existe para os testes apontarem para um servidor local.
	BaseURL string
	HTTP    *http.Client
	Log     *slog.Logger
}

type Sender struct {
	cfg Config
}

// New valida o que não pode faltar. Um remetente sem token não é um remetente
// degradado — é um silêncio com aparência de funcionamento.
func New(cfg Config) (*Sender, error) {
	if strings.TrimSpace(cfg.PhoneNumberID) == "" {
		return nil, errors.New("whatsapp: falta AIRO_WHATSAPP_PHONE_NUMBER_ID")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("whatsapp: falta AIRO_WHATSAPP_TOKEN")
	}
	if strings.TrimSpace(cfg.Template) == "" {
		return nil, errors.New("whatsapp: falta AIRO_WHATSAPP_TEMPLATE")
	}
	if cfg.Language == "" {
		cfg.Language = "pt_PT"
	}
	if cfg.GraphVersion == "" {
		cfg.GraphVersion = defaultGraphVersion
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://graph.facebook.com"
	}
	if cfg.HTTP == nil {
		// Sem prazo, um pedido pendurado segura o pedido de quem está a
		// tentar entrar — e o ecrã fica à espera sem nada a dizer.
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Sender{cfg: cfg}, nil
}

type templateRequest struct {
	MessagingProduct string   `json:"messaging_product"`
	To               string   `json:"to"`
	Type             string   `json:"type"`
	Template         template `json:"template"`
}

type template struct {
	Name       string      `json:"name"`
	Language   language    `json:"language"`
	Components []component `json:"components"`
}

type language struct {
	Code string `json:"code"`
}

type component struct {
	Type       string      `json:"type"`
	SubType    string      `json:"sub_type,omitempty"`
	Index      string      `json:"index,omitempty"`
	Parameters []parameter `json:"parameters"`
}

type parameter struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type sendResponse struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Error *graphError `json:"error"`
}

type graphError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      int    `json:"code"`
	Subcode   int    `json:"error_subcode"`
	UserTitle string `json:"error_user_title"`
	UserMsg   string `json:"error_user_msg"`
	FBTrace   string `json:"fbtrace_id"`
}

func (e graphError) Error() string {
	if e.UserMsg != "" {
		return fmt.Sprintf("whatsapp: %s (código %d)", e.UserMsg, e.Code)
	}
	return fmt.Sprintf("whatsapp: %s (código %d)", e.Message, e.Code)
}

// ErrNoWhatsApp é o caso que interessa distinguir: o número existe, mas não
// tem WhatsApp. Não é falha nossa nem da Meta, e a resposta a dar a quem está
// a tentar entrar é outra — é o SMS.
var ErrNoWhatsApp = errors.New("whatsapp: o número não tem WhatsApp")

// Send entrega o código e devolve o identificador da mensagem.
//
// O código **não** é registado, nem em erro. Um código de autenticação num
// registo é o mesmo que não ter código.
func (s *Sender) Send(ctx context.Context, phone, code, _ string) (string, error) {
	to := strings.TrimPrefix(strings.TrimSpace(phone), "+")

	body := templateRequest{
		MessagingProduct: "whatsapp",
		To:               to,
		Type:             "template",
		Template: template{
			Name:     s.cfg.Template,
			Language: language{Code: s.cfg.Language},
			Components: []component{
				{Type: "body", Parameters: []parameter{{Type: "text", Text: code}}},
				// O botão de copiar. Num template de autenticação a Meta
				// exige-o com o mesmo valor do corpo: é o que dá o "copiar
				// código" e o preenchimento automático.
				{Type: "button", SubType: "url", Index: "0",
					Parameters: []parameter{{Type: "text", Text: code}}},
			},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("whatsapp: serializar pedido: %w", err)
	}

	url := fmt.Sprintf("%s/%s/%s/messages",
		strings.TrimSuffix(s.cfg.BaseURL, "/"), s.cfg.GraphVersion, s.cfg.PhoneNumberID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("whatsapp: construir pedido: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("whatsapp: enviar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("whatsapp: ler resposta: %w", err)
	}

	var parsed sendResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("whatsapp: resposta ilegível (HTTP %d)", resp.StatusCode)
	}

	if parsed.Error != nil {
		// 131026 é "message undeliverable" — na prática, o número não tem
		// WhatsApp. Merece um erro próprio porque muda o que se faz a seguir.
		if parsed.Error.Code == 131026 || parsed.Error.Code == 131047 {
			return "", ErrNoWhatsApp
		}
		s.cfg.Log.Error("whatsapp recusou o envio",
			"code", parsed.Error.Code, "subcode", parsed.Error.Subcode,
			"type", parsed.Error.Type, "fbtrace", parsed.Error.FBTrace,
			"message", parsed.Error.Message)
		return "", *parsed.Error
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whatsapp: HTTP %d sem erro descrito", resp.StatusCode)
	}

	if len(parsed.Messages) == 0 || parsed.Messages[0].ID == "" {
		return "", errors.New("whatsapp: aceite sem identificador de mensagem")
	}

	// Aceite ≠ entregue. Quem ler este registo tem de saber a diferença, e o
	// verbo di-lo.
	s.cfg.Log.Info("código aceite pelo WhatsApp",
		"message_id", parsed.Messages[0].ID, "code_length", len(code))

	return parsed.Messages[0].ID, nil
}
