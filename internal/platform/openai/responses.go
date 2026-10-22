package openai

import "encoding/json"

// O pedido à Responses API, só com o que se usa.
type responsesRequest struct {
	Model        string         `json:"model"`
	Instructions string         `json:"instructions"`
	Input        []inputMessage `json:"input"`
	Text         textOptions    `json:"text"`
	MaxOutput    int            `json:"max_output_tokens,omitempty"`
	// Store a falso: a conversa não fica guardada do lado do fornecedor para
	// ser retomada. Não há conversa nenhuma para retomar.
	Store bool `json:"store"`
}

type inputMessage struct {
	Role    string         `json:"role"`
	Content []inputContent `json:"content"`
}

type inputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type textOptions struct {
	Format formatOptions `json:"format"`
}

type formatOptions struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

// A resposta, só com o que se lê.
type responsesResponse struct {
	ID                string       `json:"id"`
	Status            string       `json:"status"`
	Model             string       `json:"model"`
	Output            []outputItem `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

type outputItem struct {
	Type    string          `json:"type"`
	Content []outputContent `json:"content"`
}

type outputContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type errorEnvelope struct {
	Error *apiError `json:"error"`
}
