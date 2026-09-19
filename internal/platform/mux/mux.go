/*
Package mux serve os vídeos das aulas.

⚠️ **Um vídeo do Mux não tem endereço fixo.** Tem um identificador de
reprodução, e o endereço monta-se a partir dele a cada pedido — porque, com
política assinada, leva um token que expira. Guardar o endereço era guardar
uma coisa que amanhã já não abre.

Três coisas a reter:

 1. O token é **RS256** e assina-se com a chave privada que o Mux dá em base64.
    O `kid` no cabeçalho diz qual das chaves da conta é, e sem ele o Mux recusa
    sem dizer porquê.
 2. A audiência escolhe o que o token abre: `v` é o vídeo, `t` a miniatura,
    `s` o storyboard. Um token de vídeo **não** abre a miniatura — são dois.
 3. Os parâmetros de uma miniatura assinada (tempo, largura) vão **dentro** do
    token, não na query. Postos na query são ignorados, e a imagem sai com os
    valores por omissão sem ninguém perceber porquê.
*/
package mux

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	stream = "https://stream.mux.com"
	imagem = "https://image.mux.com"
)

// Config é o que o Mux precisa de saber de nós.
type Config struct {
	// TokenID e TokenSecret são as credenciais da API — criar recursos, pedir
	// o estado de um. Não têm nada a ver com a assinatura da reprodução.
	TokenID     string
	TokenSecret string

	/*
	 * SigningKeyID e SigningKeyBase64 assinam o acesso a um vídeo.
	 *
	 * São uma chave à parte, de outro tipo: a da API abre a conta inteira, e
	 * esta só deixa ver um vídeo durante uns minutos. Sem elas a reprodução é
	 * pública — e a app diz-se em modo público em vez de servir tokens
	 * inválidos.
	 */
	SigningKeyID     string
	SigningKeyBase64 string

	// WebhookSecret verifica que a notificação veio mesmo do Mux.
	WebhookSecret string

	/*
	 * TTL é quanto tempo um endereço serve.
	 *
	 * Uma hora e não cinco minutos: uma aula de quarenta minutos vista com
	 * pausas não pode ficar sem endereço a meio, e o leitor não sabe pedir
	 * outro sem recarregar o ecrã. Uma hora e não um dia porque o ponto de
	 * assinar é o endereço morrer.
	 */
	TTL time.Duration

	// ThumbnailTime é o segundo de onde sai a miniatura. Zero costuma ser o
	// preto do primeiro fotograma.
	ThumbnailTime int
}

// Client monta endereços de reprodução e fala com a API do Mux.
type Client struct {
	cfg   Config
	chave *rsa.PrivateKey
	agora func() time.Time
	// http existe para os testes porem lá um servidor deles. Nil usa um
	// cliente com prazo, montado no momento.
	http *http.Client
}

// ComHTTP troca o cliente de rede. Só os testes o usam.
func (c *Client) ComHTTP(h *http.Client) *Client { c.http = h; return c }

// ComRelogio fixa o tempo, para um token assinado num teste ser sempre igual.
func (c *Client) ComRelogio(agora func() time.Time) *Client { c.agora = agora; return c }

var (
	// ErrSemChave — pediu-se um endereço assinado sem chave configurada.
	ErrSemChave = errors.New("mux: sem chave de assinatura")
	// ErrSemPlayback — a aula não tem identificador de reprodução.
	ErrSemPlayback = errors.New("mux: aula sem identificador de reprodução")
)

/*
New lê a configuração e prepara a chave.

A chave é decifrada **aqui**, uma vez, e não a cada pedido: decifrar PEM é caro
o suficiente para se notar num ecrã que lista vinte aulas, e uma chave ilegível
tem de se descobrir ao arrancar e não à primeira pessoa que carrega em play.
*/
func New(cfg Config) (*Client, error) {
	if cfg.TTL <= 0 {
		cfg.TTL = time.Hour
	}
	c := &Client{cfg: cfg, agora: time.Now}
	if cfg.SigningKeyBase64 == "" {
		return c, nil
	}

	bruto, err := base64.StdEncoding.DecodeString(cfg.SigningKeyBase64)
	if err != nil {
		// O Mux dá a chave em base64; quem a copia da consola às vezes cola o
		// PEM directamente. Aceitam-se os dois — recusar o segundo era recusar
		// uma chave boa por causa do invólucro.
		bruto = []byte(cfg.SigningKeyBase64)
	}
	bloco, _ := pem.Decode(bruto)
	if bloco == nil {
		return nil, errors.New("mux: chave de assinatura ilegível (esperava PEM, em base64 ou em claro)")
	}
	chave, err := x509.ParsePKCS1PrivateKey(bloco.Bytes)
	if err != nil {
		generica, err2 := x509.ParsePKCS8PrivateKey(bloco.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("mux: chave de assinatura inválida: %w", err)
		}
		rsaChave, ok := generica.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("mux: a chave de assinatura não é RSA")
		}
		chave = rsaChave
	}
	c.chave = chave
	return c, nil
}

// Assina diz se há chave para assinar. Sem ela, só se servem aulas públicas.
func (c *Client) Assina() bool { return c != nil && c.chave != nil && c.cfg.SigningKeyID != "" }

// Playback é um endereço pronto a tocar, e até quando serve.
type Playback struct {
	VideoURL     string
	ThumbnailURL string
	// ExpiresAt é zero numa aula pública: não expira.
	ExpiresAt time.Time
}

/*
Playback monta o endereço de uma aula.

`assinado` vem da aula e não da configuração: a mesma conta pode ter aulas
públicas — um excerto, uma aula de demonstração — e aulas fechadas. Quem decide
é a política com que o recurso foi criado, e servi-la ao contrário dá um 403 do
lado do Mux que aparece no telemóvel como "não foi possível carregar".
*/
func (c *Client) Playback(playbackID string, assinado bool) (Playback, error) {
	if playbackID == "" {
		return Playback{}, ErrSemPlayback
	}
	if !assinado {
		return Playback{
			VideoURL: fmt.Sprintf("%s/%s.m3u8", stream, playbackID),
			ThumbnailURL: fmt.Sprintf("%s/%s/thumbnail.jpg?time=%d",
				imagem, playbackID, c.cfg.ThumbnailTime),
		}, nil
	}
	if !c.Assina() {
		return Playback{}, ErrSemChave
	}

	expira := c.agora().Add(c.cfg.TTL)
	video, err := c.token(playbackID, "v", expira, nil)
	if err != nil {
		return Playback{}, err
	}
	// Os parâmetros da miniatura viajam dentro do token. Na query são
	// ignorados, e a imagem sai do segundo zero — que é quase sempre preto.
	thumb, err := c.token(playbackID, "t", expira, map[string]any{
		"time": strconv.Itoa(c.cfg.ThumbnailTime),
	})
	if err != nil {
		return Playback{}, err
	}

	return Playback{
		VideoURL:     fmt.Sprintf("%s/%s.m3u8?token=%s", stream, playbackID, url.QueryEscape(video)),
		ThumbnailURL: fmt.Sprintf("%s/%s/thumbnail.jpg?token=%s", imagem, playbackID, url.QueryEscape(thumb)),
		ExpiresAt:    expira,
	}, nil
}

func (c *Client) token(playbackID, audiencia string, expira time.Time, extra map[string]any) (string, error) {
	claims := jwt.MapClaims{
		"sub": playbackID,
		"aud": audiencia,
		"exp": expira.Unix(),
		"kid": c.cfg.SigningKeyID,
	}
	for k, v := range extra {
		claims[k] = v
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	// O `kid` vai **também** no cabeçalho: é por lá que o Mux escolhe a chave
	// pública com que verifica. Só nas claims, a verificação falha.
	t.Header["kid"] = c.cfg.SigningKeyID
	assinado, err := t.SignedString(c.chave)
	if err != nil {
		return "", fmt.Errorf("mux: assinar token: %w", err)
	}
	return assinado, nil
}
