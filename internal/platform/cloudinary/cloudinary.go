// Package cloudinary envia imagens e devolve endereços de entrega.
//
// Sem SDK: uma assinatura SHA-1 e um formulário multipart não justificam uma
// dependência que depois é preciso acompanhar.
//
// ⚠️ **O segredo nunca sai daqui.** É por isso que a fotografia passa pelo
// servidor em vez de ir do telemóveldirectamente para a Cloudinary: uma chave de envio
// no cliente é uma chave pública, e quem a tenha pode escrever na conta.
package cloudinary

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	CloudName string
	APIKey    string
	APISecret string
	// Folder é onde as imagens vivem na conta. Uma pasta própria evita que um
	// `public_id` da Airo escreva por cima do de outro serviço da mesma conta.
	Folder string
	// BaseURL existe para os testes apontarem para um servidor local.
	BaseURL string
	// DeliveryBase é a raiz dos endereços de entrega.
	DeliveryBase string
	HTTP         *http.Client
	Now          func() time.Time
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.CloudName) == "" {
		return nil, errors.New("cloudinary: falta AIRO_CLOUDINARY_CLOUD_NAME")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("cloudinary: falta AIRO_CLOUDINARY_API_KEY")
	}
	if strings.TrimSpace(cfg.APISecret) == "" {
		return nil, errors.New("cloudinary: falta AIRO_CLOUDINARY_API_SECRET")
	}
	if cfg.Folder == "" {
		cfg.Folder = "airo/profiles"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.cloudinary.com"
	}
	if cfg.DeliveryBase == "" {
		cfg.DeliveryBase = "https://res.cloudinary.com"
	}
	if cfg.HTTP == nil {
		// Uma fotografia demora mais do que um pedido normal, e menos do que
		// alguém está disposto a esperar com o ecrã parado.
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{cfg: cfg}, nil
}

// Uploaded é o que ficou lá.
type Uploaded struct {
	PublicID string
	Version  int64
	Format   string
	Width    int
	Height   int
	// Faces é o que a Cloudinary encontrou. Vazio quer dizer que não encontrou
	// cara nenhuma — e isso muda o enquadramento.
	Faces [][]int
}

// FaceFound diz se há cara para enquadrar.
func (u Uploaded) FaceFound() bool { return len(u.Faces) > 0 }

type uploadResponse struct {
	PublicID string  `json:"public_id"`
	Version  int64   `json:"version"`
	Format   string  `json:"format"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Faces    [][]int `json:"faces"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Upload envia a imagem com um `public_id` estável.
//
// Estável e com `overwrite`: a fotografia de alguém substitui a anterior em vez
// de deixar uma cópia órfã na conta a ocupar espaço para sempre. É também o que
// torna reenviar duas vezes inofensivo.
func (c *Client) Upload(ctx context.Context, image []byte, publicID string) (Uploaded, error) {
	params := map[string]string{
		"public_id": publicID,
		"folder":    c.cfg.Folder,
		"overwrite": "true",
		// A CDN guarda o endereço antigo. Sem isto, quem trocasse a fotografia
		// continuava a ver a anterior durante horas.
		"invalidate": "true",
		// Pede a deteção de caras na resposta. É o que permite decidir o
		// enquadramento em vez de o adivinhar.
		"faces":     "true",
		"timestamp": strconv.FormatInt(c.cfg.Now().Unix(), 10),
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for k, v := range params {
		if err := form.WriteField(k, v); err != nil {
			return Uploaded{}, fmt.Errorf("cloudinary: formulário: %w", err)
		}
	}
	if err := form.WriteField("api_key", c.cfg.APIKey); err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: formulário: %w", err)
	}
	if err := form.WriteField("signature", sign(params, c.cfg.APISecret)); err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: formulário: %w", err)
	}
	part, err := form.CreateFormFile("file", "photo")
	if err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: formulário: %w", err)
	}
	if _, err := part.Write(image); err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: formulário: %w", err)
	}
	if err := form.Close(); err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: formulário: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1_1/%s/image/upload",
		strings.TrimSuffix(c.cfg.BaseURL, "/"), c.cfg.CloudName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: pedido: %w", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: enviar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: ler resposta: %w", err)
	}

	var parsed uploadResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: resposta ilegível (HTTP %d)", resp.StatusCode)
	}
	if parsed.Error != nil {
		return Uploaded{}, fmt.Errorf("cloudinary: %s", parsed.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Uploaded{}, fmt.Errorf("cloudinary: HTTP %d", resp.StatusCode)
	}
	if parsed.PublicID == "" {
		return Uploaded{}, errors.New("cloudinary: aceite sem identificador")
	}

	return Uploaded{
		PublicID: parsed.PublicID, Version: parsed.Version, Format: parsed.Format,
		Width: parsed.Width, Height: parsed.Height, Faces: parsed.Faces,
	}, nil
}

// URL monta um endereço de entrega com a transformação dada.
func (c *Client) URL(u Uploaded, transform string) string {
	format := u.Format
	if format == "" {
		format = "jpg"
	}
	return fmt.Sprintf("%s/%s/image/upload/%s/v%d/%s.%s",
		strings.TrimSuffix(c.cfg.DeliveryBase, "/"), c.cfg.CloudName,
		transform, u.Version, u.PublicID, format)
}

// Destroy apaga a imagem.
func (c *Client) Destroy(ctx context.Context, publicID string) error {
	params := map[string]string{
		"public_id":  publicID,
		"invalidate": "true",
		"timestamp":  strconv.FormatInt(c.cfg.Now().Unix(), 10),
	}
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	values.Set("api_key", c.cfg.APIKey)
	values.Set("signature", sign(params, c.cfg.APISecret))

	endpoint := fmt.Sprintf("%s/v1_1/%s/image/destroy",
		strings.TrimSuffix(c.cfg.BaseURL, "/"), c.cfg.CloudName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("cloudinary: pedido: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cloudinary: apagar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudinary: apagar: HTTP %d", resp.StatusCode)
	}
	return nil
}

// sign assina os parâmetros como a Cloudinary espera: ordenados por nome,
// juntos por `&`, com o segredo colado no fim.
//
// `api_key`, `file` e `resource_type` **não** entram — está na documentação, e
// incluí-los dá `Invalid Signature` sem dizer porquê.
func sign(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params[k])
	}
	sb.WriteString(secret)

	sum := sha1.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
