package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/intelligence"
)

// Quanto se lê de uma resposta. Uma resposta estruturada de uma refeição tem
// poucos kilobytes; um megabyte é o tecto de quem se enganou, não o normal.
const maxResponseBytes = 1 << 20

var _ intelligence.Provider = (*Client)(nil)

// GenerateStructured pede uma resposta com a forma do esquema.
func (c *Client) GenerateStructured(ctx context.Context, req intelligence.StructuredRequest) (intelligence.StructuredResult, error) {
	return c.send(ctx, req, nil)
}

// AnalyzeImage junta a imagem ao pedido, em linha.
//
// Em linha e não por endereço: o fornecedor não vai buscar nada a lado nenhum,
// e a fotografia não tem de estar acessível na internet para ser analisada.
func (c *Client) AnalyzeImage(ctx context.Context, req intelligence.ImageRequest) (intelligence.StructuredResult, error) {
	if len(req.Image) == 0 {
		return intelligence.StructuredResult{}, &intelligence.ProviderError{Kind: intelligence.Permanent, Msg: "imagem vazia"}
	}
	mime := req.MIME
	if mime == "" {
		mime = "image/jpeg"
	}
	img := inputContent{
		Type:     "input_image",
		ImageURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(req.Image),
		Detail:   "auto",
	}
	return c.send(ctx, req.StructuredRequest, &img)
}

func (c *Client) send(ctx context.Context, req intelligence.StructuredRequest, image *inputContent) (intelligence.StructuredResult, error) {
	content := []inputContent{{Type: "input_text", Text: string(req.Payload)}}
	if image != nil {
		content = append(content, *image)
	}
	body, err := json.Marshal(responsesRequest{
		Model:        c.cfg.Model,
		Instructions: req.Instructions,
		Input:        []inputMessage{{Role: "user", Content: content}},
		Text: textOptions{Format: formatOptions{
			Type: "json_schema", Name: req.SchemaName, Schema: req.Schema, Strict: true,
		}},
		MaxOutput: req.MaxTokens,
		Store:     false,
	})
	if err != nil {
		return intelligence.StructuredResult{}, &intelligence.ProviderError{Kind: intelligence.Permanent, Msg: "pedido: " + err.Error()}
	}

	return c.withRetry(ctx, func(ctx context.Context) (intelligence.StructuredResult, error) {
		return c.attempt(ctx, body)
	})
}

func (c *Client) attempt(ctx context.Context, body []byte) (intelligence.StructuredResult, error) {
	started := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return intelligence.StructuredResult{}, &intelligence.ProviderError{Kind: intelligence.Permanent, Msg: err.Error()}
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.cfg.HTTP.Do(httpReq)
	if err != nil {
		// Rede, DNS, prazo da tentativa: passageiro. O texto do erro do
		// `net/http` não leva a chave — vai no cabeçalho, não no endereço.
		return intelligence.StructuredResult{}, &intelligence.ProviderError{Kind: intelligence.Transient, Msg: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return intelligence.StructuredResult{}, &intelligence.ProviderError{Kind: intelligence.Transient, Status: resp.StatusCode, Msg: "ler resposta: " + err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return intelligence.StructuredResult{}, classify(resp.StatusCode, resp.Header, raw)
	}

	var parsed responsesResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return intelligence.StructuredResult{}, &intelligence.ProviderError{Kind: intelligence.Permanent, Status: resp.StatusCode, Msg: "resposta ilegível"}
	}
	res := intelligence.StructuredResult{
		Model:     parsed.Model,
		RequestID: resp.Header.Get("x-request-id"),
		Latency:   time.Since(started),
	}
	if res.RequestID == "" {
		res.RequestID = parsed.ID
	}
	if parsed.Usage != nil {
		res.InputTokens, res.OutputTokens = parsed.Usage.InputTokens, parsed.Usage.OutputTokens
	}

	if parsed.Error != nil {
		return res, &intelligence.ProviderError{Kind: intelligence.Permanent, Code: parsed.Error.Code, Msg: cut(parsed.Error.Message, 200)}
	}
	if parsed.Status == "incomplete" {
		reason := "desconhecido"
		if parsed.IncompleteDetails != nil && parsed.IncompleteDetails.Reason != "" {
			reason = parsed.IncompleteDetails.Reason
		}
		return res, &intelligence.ProviderError{Kind: intelligence.Incomplete, Code: reason, Msg: "resposta cortada: " + reason}
	}

	text, refusal := outputText(parsed.Output)
	if refusal != "" {
		return res, &intelligence.ProviderError{Kind: intelligence.Refused, Msg: cut(refusal, 200)}
	}
	if text == "" {
		return res, &intelligence.ProviderError{Kind: intelligence.Permanent, Msg: "resposta sem texto"}
	}
	res.Data = []byte(text)
	return res, nil
}

// outputText junta o texto das mensagens. Os itens de raciocínio, quando o
// modelo os tem, vêm antes e não são resposta — ficam de fora.
func outputText(items []outputItem) (text, refusal string) {
	var b bytes.Buffer
	for _, it := range items {
		if it.Type != "message" {
			continue
		}
		for _, c := range it.Content {
			switch c.Type {
			case "output_text":
				b.WriteString(c.Text)
			case "refusal":
				refusal += c.Refusal
			}
		}
	}
	return b.String(), refusal
}

func (c *Client) String() string { return fmt.Sprintf("openai(%s)", c.cfg.Model) }
