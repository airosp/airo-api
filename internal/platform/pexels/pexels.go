// Package pexels serve os vídeos e fotografias do acervo.
//
// ⚠️ **Existe para a chave deixar de viajar na app.** Estava no cliente com
// prefixo `EXPO_PUBLIC_`, o que quer dizer embutida em cada build: qualquer
// pessoa a extrai de um `.apk` e gasta a quota de outra. Aqui vive no servidor,
// e o telemóvel pede ao servidor.
package pexels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const base = "https://api.pexels.com"

type Client struct {
	Key  string
	HTTP *http.Client
}

func New(key string) *Client {
	return &Client{Key: key, HTTP: &http.Client{Timeout: 12 * time.Second}}
}

// Configured diz se há chave. Sem ela o resto da app funciona — o que falta são
// as imagens, e uma imagem em falta não justifica um serviço em baixo.
func (c *Client) Configured() bool { return c != nil && c.Key != "" }

type Video struct {
	ID        int    `json:"id"`
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail"`
	Duration  int    `json:"duration"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type Photo struct {
	ID  int    `json:"id"`
	URI string `json:"uri"`
	Alt string `json:"alt"`
}

type apiVideoFile struct {
	Link     string `json:"link"`
	FileType string `json:"file_type"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type apiVideo struct {
	ID       int            `json:"id"`
	Duration int            `json:"duration"`
	Image    string         `json:"image"`
	Width    int            `json:"width"`
	Height   int            `json:"height"`
	Files    []apiVideoFile `json:"video_files"`
}

/*
 * melhorFicheiro escolhe o mp4 até 1280 de largura.
 *
 * O acervo devolve o mesmo vídeo em seis tamanhos, até 4K. Num telemóvel, 4K é
 * banda gasta para pixels que o ecrã não tem — e numa rede de ginásio é o vídeo
 * a não começar.
 */
func melhorFicheiro(fs []apiVideoFile) (apiVideoFile, bool) {
	var escolhido apiVideoFile
	achou := false
	for _, f := range fs {
		if f.FileType != "video/mp4" || f.Width == 0 {
			continue
		}
		if !achou {
			escolhido, achou = f, true
			continue
		}
		// Prefere-se o maior que ainda cabe; se todos passarem, o mais pequeno.
		cabe, cabiaOAntigo := f.Width <= 1280, escolhido.Width <= 1280
		switch {
		case cabe && !cabiaOAntigo:
			escolhido = f
		case cabe == cabiaOAntigo && ((cabe && f.Width > escolhido.Width) || (!cabe && f.Width < escolhido.Width)):
			escolhido = f
		}
	}
	return escolhido, achou
}

func (c *Client) SearchVideos(ctx context.Context, query string, perPage int) ([]Video, error) {
	if perPage <= 0 || perPage > 20 {
		perPage = 5
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("orientation", "landscape")

	var payload struct {
		Videos []apiVideo `json:"videos"`
	}
	if err := c.get(ctx, "/videos/search?"+q.Encode(), &payload); err != nil {
		return nil, err
	}

	out := make([]Video, 0, len(payload.Videos))
	for _, v := range payload.Videos {
		f, ok := melhorFicheiro(v.Files)
		if !ok {
			continue
		}
		largura, altura := f.Width, f.Height
		if largura == 0 {
			largura, altura = v.Width, v.Height
		}
		out = append(out, Video{
			ID: v.ID, URL: f.Link, Thumbnail: v.Image,
			Duration: v.Duration, Width: largura, Height: altura,
		})
	}
	return out, nil
}

func (c *Client) SearchPhotos(ctx context.Context, query string, perPage int) ([]Photo, error) {
	if perPage <= 0 || perPage > 20 {
		perPage = 3
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("per_page", strconv.Itoa(perPage))

	var payload struct {
		Photos []struct {
			ID  int               `json:"id"`
			Alt string            `json:"alt"`
			Src map[string]string `json:"src"`
		} `json:"photos"`
	}
	if err := c.get(ctx, "/v1/search?"+q.Encode(), &payload); err != nil {
		return nil, err
	}

	out := make([]Photo, 0, len(payload.Photos))
	for _, p := range payload.Photos {
		// `medium` ronda os 350px no lado maior: chega para uma miniatura e
		// para o herói da refeição, e não gasta a rede de quem está no ginásio.
		uri := p.Src["medium"]
		if uri == "" {
			uri = p.Src["small"]
		}
		if uri == "" {
			continue
		}
		out = append(out, Photo{ID: p.ID, URI: uri, Alt: p.Alt})
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, caminho string, destino any) error {
	if !c.Configured() {
		return fmt.Errorf("sem chave do acervo")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+caminho, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.Key)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("acervo inacessível: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("o acervo respondeu %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(destino)
}
