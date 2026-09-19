package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

/*
 * A API do Mux — criar recursos e saber o que é feito deles.
 *
 * ⚠️ **Um vídeo não se envia para aqui.** Pede-se um destino de envio, o Mux
 * devolve um endereço, e o ficheiro vai directo do computador de quem filmou
 * para o Mux. O ficheiro nunca passa por esta API: uma aula de quarenta
 * minutos são centenas de megabytes, e pô-los a atravessar um contentor de
 * 512 MB de memória é derrubá-lo com um upload.
 *
 * O `passthrough` é o que amarra as duas pontas: leva o id da aula, e o Mux
 * devolve-o na notificação. Sem ele, um recurso pronto é um vídeo sem dono.
 */

const apiBase = "https://api.mux.com"

// ErrSemCredenciais — a API do Mux foi chamada sem credenciais configuradas.
var ErrSemCredenciais = errors.New("mux: sem credenciais de API")

// Envio é um destino de upload, para quem tem o ficheiro.
type Envio struct {
	// ID do upload, para se poder perguntar por ele antes de haver recurso.
	ID string
	// URL é para onde o ficheiro vai, por `PUT`. Vale uma hora.
	URL string
}

/*
CriarEnvio pede ao Mux um destino de upload para uma aula.

`cors` é a origem que vai fazer o envio pelo browser — o painel. Vazio quando o
envio é feito por uma ferramenta de linha de comandos, que não tem origem.
*/
func (c *Client) CriarEnvio(ctx context.Context, aulaID, cors string, assinado bool) (Envio, error) {
	politica := "public"
	if assinado {
		politica = "signed"
	}
	corpo := map[string]any{
		"cors_origin": cors,
		"new_asset_settings": map[string]any{
			"playback_policy": []string{politica},
			"passthrough":     aulaID,
			// `baseline` porque o que se vê numa aula é um corpo a mexer, não
			// texto miúdo: o perfil alto custa mais a codificar e a entregar
			// sem diferença visível num telemóvel.
			"encoding_tier": "baseline",
		},
	}

	var resposta struct {
		Data struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := c.pedir(ctx, http.MethodPost, "/video/v1/uploads", corpo, &resposta); err != nil {
		return Envio{}, err
	}
	return Envio{ID: resposta.Data.ID, URL: resposta.Data.URL}, nil
}

// Recurso é o estado de um vídeo no Mux.
type Recurso struct {
	ID          string
	Estado      string
	Passthrough string
	PlaybackID  string
	Politica    string
	Duracao     float64
	Largura     int
	Altura      int
}

/*
Recurso lê o estado de um vídeo.

Existe para o caso de a notificação se perder — um webhook é uma entrega best
effort, e uma aula presa em "a processar" para sempre porque uma notificação se
perdeu é um defeito que só se resolve a perguntar.
*/
func (c *Client) Recurso(ctx context.Context, assetID string) (Recurso, error) {
	var resposta struct {
		Data struct {
			ID          string  `json:"id"`
			Status      string  `json:"status"`
			Passthrough string  `json:"passthrough"`
			Duration    float64 `json:"duration"`
			PlaybackIDs []struct {
				ID     string `json:"id"`
				Policy string `json:"policy"`
			} `json:"playback_ids"`
			Tracks []struct {
				Type      string `json:"type"`
				MaxWidth  int    `json:"max_width"`
				MaxHeight int    `json:"max_height"`
			} `json:"tracks"`
		} `json:"data"`
	}
	if err := c.pedir(ctx, http.MethodGet, "/video/v1/assets/"+assetID, nil, &resposta); err != nil {
		return Recurso{}, err
	}

	out := Recurso{
		ID: resposta.Data.ID, Estado: resposta.Data.Status,
		Passthrough: resposta.Data.Passthrough, Duracao: resposta.Data.Duration,
	}
	for _, p := range resposta.Data.PlaybackIDs {
		if p.ID != "" {
			out.PlaybackID, out.Politica = p.ID, p.Policy
			break
		}
	}
	for _, t := range resposta.Data.Tracks {
		if t.Type == "video" {
			out.Largura, out.Altura = t.MaxWidth, t.MaxHeight
			break
		}
	}
	return out, nil
}

// Apagar remove um recurso do Mux. Existe para quando uma aula é retirada: um
// vídeo que já não se serve continua a custar armazenamento todos os meses.
func (c *Client) Apagar(ctx context.Context, assetID string) error {
	return c.pedir(ctx, http.MethodDelete, "/video/v1/assets/"+assetID, nil, nil)
}

func (c *Client) pedir(ctx context.Context, metodo, caminho string, corpo, out any) error {
	if c.cfg.TokenID == "" || c.cfg.TokenSecret == "" {
		return ErrSemCredenciais
	}

	var body io.Reader
	if corpo != nil {
		bruto, err := json.Marshal(corpo)
		if err != nil {
			return fmt.Errorf("mux: serializar pedido: %w", err)
		}
		body = bytes.NewReader(bruto)
	}

	req, err := http.NewRequestWithContext(ctx, metodo, apiBase+caminho, body)
	if err != nil {
		return fmt.Errorf("mux: montar pedido: %w", err)
	}
	req.SetBasicAuth(c.cfg.TokenID, c.cfg.TokenSecret)
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	cliente := c.http
	if cliente == nil {
		// Vinte segundos: criar um envio é rápido, e um pedido pendurado a
		// segurar um handler é pior do que um erro que se vê.
		cliente = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := cliente.Do(req)
	if err != nil {
		return fmt.Errorf("mux: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// O corpo do erro do Mux diz o que está mal — e é limitado a 4 KB
		// porque um erro não é um sítio para ler megabytes para um registo.
		detalhe, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mux: %s %s devolveu %d: %s", metodo, caminho, resp.StatusCode, detalhe)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mux: resposta ilegível: %w", err)
	}
	return nil
}
