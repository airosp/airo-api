package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/intelligence"
)

func client(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var waits []time.Duration
	c, err := New(Config{
		APIKey: "sk-teste", Model: "modelo-de-teste", BaseURL: srv.URL,
		Timeout: 2 * time.Second, MaxRetries: 2,
		Sleep:  func(d time.Duration) { waits = append(waits, d) },
		Jitter: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, &waits
}

const okBody = `{"id":"resp_1","status":"completed","model":"modelo-de-teste-2026",
 "output":[{"type":"reasoning","content":[]},
           {"type":"message","content":[{"type":"output_text","text":"{\"containsFood\":true,\"items\":[]}"}]}],
 "usage":{"input_tokens":1200,"output_tokens":80}}`

func req() intelligence.StructuredRequest {
	return intelligence.StructuredRequest{
		Task: "meal_text", SchemaName: "meal_extraction",
		Schema:       json.RawMessage(`{"type":"object"}`),
		Instructions: "regras", Payload: []byte("comi xima"), MaxTokens: 500,
	}
}

func TestPedidoLevaOQueAAPIPede(t *testing.T) {
	var got map[string]any
	c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer sk-teste" {
			t.Errorf("%s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("x-request-id", "req_abc")
		_, _ = io.WriteString(w, okBody)
	})
	res, err := c.AnalyzeImage(context.Background(), intelligence.ImageRequest{
		StructuredRequest: req(), Image: []byte{0xff, 0xd8, 0xff}, MIME: "image/jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Data) != `{"containsFood":true,"items":[]}` {
		t.Errorf("dados %q", res.Data)
	}
	if res.InputTokens != 1200 || res.OutputTokens != 80 || res.RequestID != "req_abc" || res.Model != "modelo-de-teste-2026" {
		t.Errorf("%+v", res)
	}
	if got["store"] != false || got["model"] != "modelo-de-teste" {
		t.Errorf("store/model: %v %v", got["store"], got["model"])
	}
	format := got["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "meal_extraction" {
		t.Errorf("formato %v", format)
	}
	content := got["input"].([]any)[0].(map[string]any)["content"].([]any)
	img := content[1].(map[string]any)
	if img["type"] != "input_image" || !strings.HasPrefix(img["image_url"].(string), "data:image/jpeg;base64,") {
		t.Errorf("imagem %v", img)
	}
}

func TestFalhaPassageiraTentaOutraVez(t *testing.T) {
	var n atomic.Int32
	c, waits := client(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"Rate limit","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
			return
		}
		_, _ = io.WriteString(w, okBody)
	})
	if _, err := c.GenerateStructured(context.Background(), req()); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Errorf("%d tentativas", n.Load())
	}
	if len(*waits) != 2 || (*waits)[0] != time.Second {
		t.Errorf("esperas %v — tem de respeitar o Retry-After", *waits)
	}
}

func TestErroPermanenteNaoRepete(t *testing.T) {
	for nome, caso := range map[string]struct {
		status int
		body   string
		kind   intelligence.Kind
	}{
		"chave errada":    {401, `{"error":{"message":"Incorrect API key","code":"invalid_api_key"}}`, intelligence.Permanent},
		"pedido errado":   {400, `{"error":{"message":"Invalid schema","code":"invalid_json_schema"}}`, intelligence.Permanent},
		"conta sem saldo": {429, `{"error":{"message":"You exceeded your current quota","code":"insufficient_quota"}}`, intelligence.Quota},
	} {
		var n atomic.Int32
		c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(caso.status)
			_, _ = io.WriteString(w, caso.body)
		})
		_, err := c.GenerateStructured(context.Background(), req())
		if intelligence.KindOf(err) != caso.kind {
			t.Errorf("%s: %v", nome, err)
		}
		if n.Load() != 1 {
			t.Errorf("%s: %d tentativas — não melhora com repetição", nome, n.Load())
		}
	}
}

// Uma resposta que chegou é uma operação concluída: não se paga duas vezes.
func TestRespostaCortadaOuRecusadaNaoRepete(t *testing.T) {
	for nome, caso := range map[string]struct {
		body string
		kind intelligence.Kind
	}{
		"cortada":  {`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":10,"output_tokens":500}}`, intelligence.Incomplete},
		"recusada": {`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"Não posso ajudar."}]}]}`, intelligence.Refused},
	} {
		var n atomic.Int32
		c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			_, _ = io.WriteString(w, caso.body)
		})
		res, err := c.GenerateStructured(context.Background(), req())
		if intelligence.KindOf(err) != caso.kind || n.Load() != 1 {
			t.Errorf("%s: %v, %d tentativas", nome, err, n.Load())
		}
		if nome == "cortada" && res.OutputTokens != 500 {
			t.Errorf("o consumo de uma resposta cortada também conta: %+v", res)
		}
	}
}

func TestCancelarParaJa(t *testing.T) {
	c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GenerateStructured(ctx, req())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestConfiguracaoObrigatoria(t *testing.T) {
	if _, err := New(Config{Model: "x"}); err == nil {
		t.Error("sem chave não se constrói")
	}
	if _, err := New(Config{APIKey: "sk"}); err == nil {
		t.Error("sem modelo não se constrói — escolhê-lo é uma decisão medida")
	}
}

// A mensagem de erro não leva a chave.
func TestErroNaoExpoeAChave(t *testing.T) {
	c, _ := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided: sk-teste","code":"invalid_api_key"}}`)
	})
	_, err := c.GenerateStructured(context.Background(), req())
	var pe *intelligence.ProviderError
	if !errors.As(err, &pe) || pe.Code != "invalid_api_key" {
		t.Fatalf("%v", err)
	}
}
