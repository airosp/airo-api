package mux_test

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/mux"
	"github.com/golang-jwt/jwt/v5"
)

func chaveDeTeste(t *testing.T) (string, *rsa.PublicKey) {
	t.Helper()
	// 2048 e não 4096: gerar a chave é o que este teste demora, e o que se
	// prova é a forma do token, não a força da chave.
	chave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(chave),
	})
	return base64.StdEncoding.EncodeToString(pemBytes), &chave.PublicKey
}

/*
 * O endereço assinado leva um token que o Mux sabe verificar.
 *
 * ⚠️ Três coisas costumam sair mal e nenhuma dá erro do nosso lado — dão 403
 * do lado do Mux, que no telemóvel aparece como "não foi possível carregar":
 * o `kid` só nas claims e não no cabeçalho, a audiência errada, e a miniatura
 * assinada com o token do vídeo.
 */
func TestEnderecoAssinadoLevaTokenVerificavel(t *testing.T) {
	chaveB64, publica := chaveDeTeste(t)
	agora := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	c, err := mux.New(mux.Config{
		SigningKeyID: "chave-123", SigningKeyBase64: chaveB64,
		TTL: time.Hour, ThumbnailTime: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.ComRelogio(func() time.Time { return agora })

	p, err := c.Playback("abc123", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.VideoURL, "https://stream.mux.com/abc123.m3u8?token=") {
		t.Fatalf("endereço de vídeo inesperado: %s", p.VideoURL)
	}
	if !p.ExpiresAt.Equal(agora.Add(time.Hour)) {
		t.Errorf("expira em %v, esperava %v", p.ExpiresAt, agora.Add(time.Hour))
	}

	verificar := func(bruto, audiencia string) jwt.MapClaims {
		t.Helper()
		tok, err := jwt.Parse(bruto, func(*jwt.Token) (any, error) { return publica, nil },
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithAudience(audiencia),
			jwt.WithTimeFunc(func() time.Time { return agora }))
		if err != nil {
			t.Fatalf("token de %q não verifica: %v", audiencia, err)
		}
		if kid, _ := tok.Header["kid"].(string); kid != "chave-123" {
			t.Errorf("o `kid` do cabeçalho é %q — o Mux escolhe a chave por ele", kid)
		}
		claims, _ := tok.Claims.(jwt.MapClaims)
		if claims["sub"] != "abc123" {
			t.Errorf("sub = %v, esperava o identificador de reprodução", claims["sub"])
		}
		return claims
	}

	verificar(tokenDe(t, p.VideoURL), "v")
	claimsThumb := verificar(tokenDe(t, p.ThumbnailURL), "t")
	// O tempo da miniatura vai **dentro** do token: na query é ignorado.
	if claimsThumb["time"] != "3" {
		t.Errorf("o tempo da miniatura devia viajar no token, veio %v", claimsThumb["time"])
	}
}

// Sem chave, o endereço é público e não leva token nenhum.
func TestEnderecoPublicoNaoLevaToken(t *testing.T) {
	c, err := mux.New(mux.Config{ThumbnailTime: 3})
	if err != nil {
		t.Fatal(err)
	}
	if c.Assina() {
		t.Fatal("sem chave configurada não devia assinar")
	}
	p, err := c.Playback("abc123", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.VideoURL, "token=") {
		t.Errorf("um endereço público não leva token: %s", p.VideoURL)
	}
	if !p.ExpiresAt.IsZero() {
		t.Error("um endereço público não expira")
	}
}

// Pedir um endereço assinado sem chave é um erro — e não um endereço público
// servido em silêncio, que deixaria uma aula fechada aberta a toda a gente.
func TestAssinadoSemChaveRecusa(t *testing.T) {
	c, _ := mux.New(mux.Config{})
	if _, err := c.Playback("abc123", true); err == nil {
		t.Fatal("esperava recusa ao assinar sem chave")
	}
}

func tokenDe(t *testing.T, bruto string) string {
	t.Helper()
	u, err := url.Parse(bruto)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("endereço sem token: %s", bruto)
	}
	return tok
}

/* ── o webhook ───────────────────────────────────────────────────────────── */

func assinar(segredo string, quando time.Time, corpo string) string {
	ts := fmt.Sprintf("%d", quando.Unix())
	mac := hmac.New(sha256.New, []byte(segredo))
	mac.Write([]byte(ts + "." + corpo))
	return fmt.Sprintf("t=%s,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestAssinaturaDoWebhook(t *testing.T) {
	agora := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	corpo := `{"type":"video.asset.ready","data":{"id":"asset-1"}}`

	if err := mux.VerificarAssinatura(assinar("segredo", agora, corpo), []byte(corpo), "segredo", agora); err != nil {
		t.Fatalf("uma assinatura boa devia passar: %v", err)
	}

	// Corpo adulterado depois de assinado.
	adulterado := `{"type":"video.asset.ready","data":{"id":"asset-2"}}`
	if err := mux.VerificarAssinatura(assinar("segredo", agora, corpo), []byte(adulterado), "segredo", agora); err == nil {
		t.Error("um corpo trocado devia ser recusado")
	}

	// Outro segredo.
	if err := mux.VerificarAssinatura(assinar("outro", agora, corpo), []byte(corpo), "segredo", agora); err == nil {
		t.Error("uma assinatura de outro segredo devia ser recusada")
	}

	// Repetida uma hora depois: a assinatura continua válida, o tempo não.
	velha := assinar("segredo", agora.Add(-time.Hour), corpo)
	if err := mux.VerificarAssinatura(velha, []byte(corpo), "segredo", agora); err == nil {
		t.Error("uma notificação de há uma hora devia cair fora da janela")
	}

	// Sem segredo configurado não passa nada.
	if err := mux.VerificarAssinatura(assinar("", agora, corpo), []byte(corpo), "", agora); err == nil {
		t.Error("sem segredo configurado, o webhook não pode aceitar nada")
	}
}

func TestLerEventoDeRecursoPronto(t *testing.T) {
	corpo := []byte(`{
	  "type": "video.asset.ready",
	  "data": {
	    "id": "asset-1", "status": "ready", "passthrough": "aula-tronco",
	    "duration": 2412.5,
	    "playback_ids": [{"id": "playback-1", "policy": "signed"}],
	    "tracks": [
	      {"type": "audio"},
	      {"type": "video", "max_width": 1920, "max_height": 1080}
	    ]
	  }
	}`)
	e, err := mux.LerEvento(corpo)
	if err != nil {
		t.Fatal(err)
	}
	if e.Tipo != "video.asset.ready" || e.Dados.Passthrough != "aula-tronco" {
		t.Fatalf("evento mal lido: %+v", e.Dados)
	}
	id, politica, ok := e.Playback()
	if !ok || id != "playback-1" || politica != "signed" {
		t.Errorf("reprodução = (%q,%q,%v)", id, politica, ok)
	}
	l, a, ok := e.Dimensoes()
	// A faixa de áudio vem primeiro de propósito: é dela que saíam zeros
	// quando se lia a primeira em vez de procurar a de vídeo.
	if !ok || l != 1920 || a != 1080 {
		t.Errorf("dimensões = (%d,%d,%v)", l, a, ok)
	}
}
