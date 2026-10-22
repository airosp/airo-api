// Package openai fala com a Responses API da OpenAI.
//
// Implementa `intelligence.Provider` e mais nada: não sabe o que é uma
// refeição, nem um consentimento, nem um orçamento. Recebe instruções, dados e
// um esquema; devolve bytes e o que custaram.
package openai

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openai.com/v1"

type Config struct {
	APIKey string
	// Model não tem valor por omissão, de propósito: escolher o modelo é
	// decidir a qualidade do reconhecimento, e essa decisão tem de ser medida
	// com pratos de cá antes de ser tomada. Ver `cmd/avaliar-ia`.
	Model string
	// BaseURL existe para os testes apontarem para um servidor local.
	BaseURL string
	// Timeout é o prazo de **cada** tentativa, não da operação inteira.
	Timeout time.Duration
	// MaxRetries são as tentativas a mais depois da primeira, só para falhas
	// passageiras.
	MaxRetries int
	HTTP       *http.Client
	// Sleep e Jitter existem para os testes não esperarem de verdade.
	Sleep  func(time.Duration)
	Jitter func() float64
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openai: falta AIRO_OPENAI_API_KEY")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("openai: falta AIRO_OPENAI_MODEL")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.HTTP == nil {
		// Sem `Timeout` no cliente: o prazo vem do contexto de cada tentativa,
		// e dois prazos a competir dão erros que não dizem qual disparou.
		cfg.HTTP = &http.Client{}
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	if cfg.Jitter == nil {
		cfg.Jitter = rand.Float64
	}
	return &Client{cfg: cfg}, nil
}

// Model é o modelo configurado, para a auditoria e para o arranque o dizer.
func (c *Client) Model() string { return c.cfg.Model }
