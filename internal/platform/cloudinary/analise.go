package cloudinary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

/*
 * A versão de uma fotografia que vai ser analisada.
 *
 *	c_limit,w_1024,h_1024   reduz sem cortar: o modelo tem de ver o prato
 *	                        inteiro, e o enquadramento da app corta as bordas
 *	f_jpg                   formato fixo — `f_auto` muda com quem pede, e o
 *	                        fornecedor tem de receber um que conheça
 *
 * Uma versão transformada é servida **sem os metadados** do ficheiro original:
 * a localização do telemóvel, a hora, o modelo do aparelho ficam na Cloudinary
 * e não saem para o fornecedor.
 */
const paraAnalise = "c_limit,w_1024,h_1024,q_auto,f_jpg"

// MaxFetchBytes é o tecto de uma fotografia descarregada para análise.
const MaxFetchBytes = 8 << 20

// ErrNotOwned é um endereço que não é uma fotografia de refeição desta pessoa.
var ErrNotOwned = errors.New("cloudinary: a fotografia não pertence a quem pede")

/*
 * MealAnalysisURL confirma que a fotografia é de quem pede e devolve o
 * endereço da versão a analisar.
 *
 * O telemóvel manda o endereço que recebeu ao guardar a fotografia. Antes de o
 * usar, verifica-se tudo o que se pode verificar sem perguntar a ninguém: que
 * é desta conta, desta pasta, da pasta **desta pessoa**, e com um nome com a
 * forma dos que a Airo dá. Um endereço qualquer da internet não passa, e o de
 * outra pessoa também não.
 */
func (c *Client) MealAnalysisURL(delivered, userID string) (string, error) {
	base := strings.TrimSuffix(c.cfg.DeliveryBase, "/") + "/" + c.cfg.CloudName + "/image/upload/"
	if !strings.HasPrefix(delivered, base) || userID == "" {
		return "", ErrNotOwned
	}
	re := regexp.MustCompile(`^(?:[^/]+/)?v(\d+)/(` + regexp.QuoteMeta(c.cfg.Folder) + `/` +
		regexp.QuoteMeta(userID) + `/[a-f0-9]{24})\.[a-z0-9]+$`)
	m := re.FindStringSubmatch(strings.TrimPrefix(delivered, base))
	if m == nil {
		return "", ErrNotOwned
	}
	return fmt.Sprintf("%s%s/v%s/%s.jpg", base, paraAnalise, m[1], m[2]), nil
}

// Fetch descarrega uma imagem servida pela Cloudinary, com tecto de tamanho.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("cloudinary: pedido: %w", err)
	}
	req.Header.Set("Accept", "image/jpeg")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("cloudinary: descarregar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("cloudinary: HTTP %d ao descarregar", resp.StatusCode)
	}
	mime := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", fmt.Errorf("cloudinary: não é uma imagem (%s)", mime)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("cloudinary: ler imagem: %w", err)
	}
	if len(body) > MaxFetchBytes {
		return nil, "", errors.New("cloudinary: imagem demasiado grande")
	}
	return body, strings.TrimSpace(strings.Split(mime, ";")[0]), nil
}
