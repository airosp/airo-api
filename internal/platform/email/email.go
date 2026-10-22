// Package email entrega emails pela Metigan.
//
// Hoje só o painel da equipa o usa: o código de entrada e o aviso a quem é
// adicionado. Um email com um destinatário é transaccional na Metigan — sai
// logo e os links não são reescritos para contar cliques.
//
// Como no WhatsApp e no SMS, o `success` da resposta diz **aceite**, não
// **entregue**.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	// APIKey é a chave `mtg_live_…`. Fica no servidor, nunca no browser.
	APIKey string
	// From é o remetente, com nome: `Airo <noreply@savanapoint.com>`.
	From string
	// BaseURL existe para os testes apontarem para um servidor local.
	BaseURL string
	HTTP    *http.Client
}

type Client struct{ cfg Config }

func New(cfg Config) *Client {
	if cfg.HTTP == nil {
		// Quem pediu o código está a olhar para o ecrã.
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.metigan.io"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg}
}

// Configured diz se há com que enviar.
func (c *Client) Configured() bool {
	return c != nil && c.cfg.APIKey != "" && c.cfg.From != ""
}

// Message é um email para uma pessoa.
type Message struct {
	To      string
	Subject string
	HTML    string
}

type sendBody struct {
	From       string   `json:"from"`
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`
	Content    string   `json:"content"`
}

type sendReply struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	Error        string `json:"error"`
	FailedEmails []struct {
		Recipient string `json:"recipient"`
		Error     string `json:"error"`
	} `json:"failedEmails"`
}

// Send entrega um email. Um erro diz que a Metigan não o aceitou.
func (c *Client) Send(ctx context.Context, m Message) error {
	if !c.Configured() {
		return errors.New("email: sem chave ou remetente")
	}
	body, err := json.Marshal(sendBody{
		From: c.cfg.From, Recipients: []string{m.To}, Subject: m.Subject, Content: m.HTML,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/api/email/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	res, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("email: metigan: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var reply sendReply
	_ = json.Unmarshal(raw, &reply)
	if res.StatusCode >= 300 {
		why := reply.Message
		if why == "" {
			why = reply.Error
		}
		return fmt.Errorf("email: metigan respondeu %d: %s", res.StatusCode, why)
	}
	if len(reply.FailedEmails) > 0 {
		return fmt.Errorf("email: metigan recusou o destinatário: %s", reply.FailedEmails[0].Error)
	}
	if !reply.Success {
		return fmt.Errorf("email: metigan não aceitou: %s", reply.Message)
	}
	return nil
}
