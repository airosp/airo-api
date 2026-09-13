package whatsapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/airosp/airo-api/internal/platform/whatsapp"
)

func newSender(t *testing.T, h http.HandlerFunc) *whatsapp.Sender {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s, err := whatsapp.New(whatsapp.Config{
		PhoneNumberID: "183024074892062",
		Token:         "token-de-teste",
		Template:      "otp_auth",
		BaseURL:       srv.URL,
		Log:           quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnvioMontaOPedidoQueAMetaEspera(t *testing.T) {
	var got map[string]any
	var path, auth string

	s := newSender(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.TESTE"}]}`))
	})

	id, err := s.Send(context.Background(), "+258841234567", "483920", "whatsapp")
	if err != nil {
		t.Fatal(err)
	}
	if id != "wamid.TESTE" {
		t.Fatalf("identificador = %q", id)
	}
	if path != "/v23.0/183024074892062/messages" {
		t.Fatalf("caminho = %q", path)
	}
	if auth != "Bearer token-de-teste" {
		t.Fatalf("autorização = %q", auth)
	}

	// O `+` sai. Com ele a Meta aceita e não entrega — é o género de detalhe
	// que só se descobre quando alguém não recebe o código.
	if got["to"] != "258841234567" {
		t.Fatalf("destinatário = %v, devia ir sem +", got["to"])
	}
	if got["type"] != "template" {
		t.Fatalf("tipo = %v", got["type"])
	}

	tpl := got["template"].(map[string]any)
	if tpl["name"] != "otp_auth" {
		t.Fatalf("template = %v", tpl["name"])
	}
	if tpl["language"].(map[string]any)["code"] != "en_US" {
		t.Fatalf("língua = %v", tpl["language"])
	}

	// Corpo **e** botão, com o mesmo código: é o par que dá o "copiar código".
	comps := tpl["components"].([]any)
	if len(comps) != 2 {
		t.Fatalf("componentes = %d, esperava corpo e botão", len(comps))
	}
	for _, c := range comps {
		m := c.(map[string]any)
		p := m["parameters"].([]any)[0].(map[string]any)
		if p["text"] != "483920" {
			t.Fatalf("componente %v levou %v", m["type"], p["text"])
		}
	}
	if comps[1].(map[string]any)["sub_type"] != "url" {
		t.Fatal("o botão tem de ser sub_type url")
	}
}

// O número sem WhatsApp não é uma falha nossa, e o que se faz a seguir é
// diferente — por isso tem erro próprio.
func TestNumeroSemWhatsAppTemErroProprio(t *testing.T) {
	for _, code := range []int{131026, 131047} {
		s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"undeliverable","code":` +
				json.Number(itoa(code)).String() + `}}`))
		})
		_, err := s.Send(context.Background(), "+258840000000", "123456", "whatsapp")
		if !errors.Is(err, whatsapp.ErrNoWhatsApp) {
			t.Fatalf("código %d devia dar ErrNoWhatsApp; deu %v", code, err)
		}
	}
}

func TestErroDaMetaChegaComOCodigo(t *testing.T) {
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Template name does not exist","code":132001}}`))
	})
	_, err := s.Send(context.Background(), "+258841234567", "123456", "whatsapp")
	if err == nil {
		t.Fatal("um template inexistente tem de falhar")
	}
	if !strings.Contains(err.Error(), "132001") {
		t.Fatalf("o erro tem de trazer o código da Meta: %v", err)
	}
}

// Um 200 sem identificador é um sucesso por engano: não houve mensagem.
func TestAceiteSemIdentificadorEFalha(t *testing.T) {
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"messages":[]}`))
	})
	if _, err := s.Send(context.Background(), "+258841234567", "123456", "whatsapp"); err == nil {
		t.Fatal("sem identificador de mensagem tem de falhar")
	}
}

// O código nunca vai para o registo — nem quando corre bem.
func TestOCodigoNaoAparaceNoRegisto(t *testing.T) {
	var buf strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.X"}]}`))
	}))
	t.Cleanup(srv.Close)

	s, err := whatsapp.New(whatsapp.Config{
		PhoneNumberID: "1", Token: "t", Template: "otp_auth",
		BaseURL: srv.URL, Log: logTo(&buf),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), "+258841234567", "483920", "whatsapp"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "483920") {
		t.Fatalf("o código foi para o registo: %s", buf.String())
	}
}

func TestConfiguracaoIncompletaRecusa(t *testing.T) {
	casos := []struct {
		nome string
		cfg  whatsapp.Config
	}{
		{"sem número", whatsapp.Config{Token: "t", Template: "x"}},
		{"sem token", whatsapp.Config{PhoneNumberID: "1", Template: "x"}},
		{"sem template", whatsapp.Config{PhoneNumberID: "1", Token: "t"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if _, err := whatsapp.New(c.cfg); err == nil {
				t.Fatal("devia recusar: um remetente incompleto é um silêncio")
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// O serviço distingue os dois casos por esta interface. Se ela desaparecer, o
// número sem WhatsApp volta a ser "tenta outra vez" — e nunca funciona.
func TestNumeroSemWhatsAppDeclaraSeNaoEntregavel(t *testing.T) {
	var u interface{ Undeliverable() bool }
	if !errors.As(error(whatsapp.ErrNoWhatsApp), &u) {
		t.Fatal("ErrNoWhatsApp tem de implementar Undeliverable()")
	}
	if !u.Undeliverable() {
		t.Fatal("Undeliverable() devia ser verdadeiro")
	}

	// E um erro qualquer da Meta **não** é não-entregável: esse melhora com
	// uma segunda tentativa.
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"temporário","code":131000}}`))
	})
	_, err := s.Send(context.Background(), "+258841234567", "123456", "whatsapp")
	if errors.As(err, &u) {
		t.Fatalf("erro transitório não devia ser não-entregável: %v", err)
	}
}

// O código de erro da Meta tem de aparecer no registo.
//
// Chamava-se `code`, e o `scrub` ocultava-o — a linha dizia `code=[oculto]`
// sobre a única coisa accionável que ali havia. A ocultação está certa; o nome
// é que estava errado.
func TestOCodigoDeErroDaMetaApareceNoRegisto(t *testing.T) {
	var buf strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"(#132001) Template name does not exist in the translation","code":132001}}`))
	}))
	t.Cleanup(srv.Close)

	s, err := whatsapp.New(whatsapp.Config{
		PhoneNumberID: "1", Token: "t", Template: "otp_auth",
		BaseURL: srv.URL, Log: logTo(&buf),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), "+258841234567", "483920", "whatsapp"); err == nil {
		t.Fatal("132001 tem de falhar")
	}

	registo := buf.String()
	if !strings.Contains(registo, "meta_code=132001") {
		t.Fatalf("o código da Meta tem de estar legível: %s", registo)
	}
	// E diz qual a variável a corrigir, porque repetir nunca vai funcionar.
	if !strings.Contains(registo, "AIRO_WHATSAPP_TEMPLATE") {
		t.Fatalf("o registo tem de dizer o que corrigir: %s", registo)
	}
	if !strings.Contains(registo, "otp_auth") {
		t.Fatalf("o registo tem de dizer que template falhou: %s", registo)
	}
	// O código de autenticação continua fora do registo, mesmo em erro.
	if strings.Contains(registo, "483920") {
		t.Fatalf("o código foi para o registo: %s", registo)
	}
}

// A versão do Graph é a mesma que o serviço de WhatsApp já usa com esta conta.
func TestVersaoDoGraphPorOmissao(t *testing.T) {
	var path string
	s := newSender(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.X"}]}`))
	})
	if _, err := s.Send(context.Background(), "+258841234567", "123456", "whatsapp"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, "/v23.0/") {
		t.Fatalf("caminho = %q, esperava v23.0", path)
	}
}

// "Está em inglês" não diz se é `en` ou `en_US`. Cada palpite errado custava
// uma ida ao servidor e uma pessoa de fora — por isso tenta-se.
//
// Um 132001 quer dizer que **nada foi enviado**: a tentativa seguinte custa uma
// chamada e nunca uma segunda mensagem.
func TestLinguaDeRecursoQuandoAPrimeiraNaoExiste(t *testing.T) {
	var tentadas []string
	var buf strings.Builder

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		lang := req["template"].(map[string]any)["language"].(map[string]any)["code"].(string)
		tentadas = append(tentadas, lang)

		if lang != "en_US" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"(#132001) Template name does not exist in the translation","code":132001}}`))
			return
		}
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OK"}]}`))
	}))
	t.Cleanup(srv.Close)

	s, err := whatsapp.New(whatsapp.Config{
		PhoneNumberID: "1", Token: "t", Template: "otp_auth",
		Language: "pt_BR", Fallbacks: []string{"en", "en_US"},
		BaseURL: srv.URL, Log: logTo(&buf),
	})
	if err != nil {
		t.Fatal(err)
	}

	id, err := s.Send(context.Background(), "+258841234567", "483920", "whatsapp")
	if err != nil {
		t.Fatal(err)
	}
	if id != "wamid.OK" {
		t.Fatalf("identificador = %q", id)
	}
	if len(tentadas) != 3 || tentadas[0] != "pt_BR" || tentadas[2] != "en_US" {
		t.Fatalf("línguas tentadas = %v", tentadas)
	}
	// E diz qual fixar, para não se ficar a depender da tentativa.
	if !strings.Contains(buf.String(), "AIRO_WHATSAPP_LANGUAGE=en_US") {
		t.Fatalf("o registo tem de dizer o valor a fixar: %s", buf.String())
	}
}

// Um erro que não é de língua não se repete noutra língua: o número inválido
// continua inválido, e insistir só gasta chamadas.
func TestOutroErroNaoTentaOutraLingua(t *testing.T) {
	tentativas := 0
	s := newSender(t, func(w http.ResponseWriter, _ *http.Request) {
		tentativas++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid recipient","code":131026}}`))
	})
	_, err := s.Send(context.Background(), "+258840000000", "123456", "whatsapp")
	if !errors.Is(err, whatsapp.ErrNoWhatsApp) {
		t.Fatalf("erro = %v", err)
	}
	if tentativas != 1 {
		t.Fatalf("%d tentativas: só devia tentar uma vez", tentativas)
	}
}

// Quando nenhuma língua existe, o erro que chega é o da língua configurada —
// não o da última alternativa, que ninguém pediu.
func TestNenhumaLinguaExisteDevolveOPrimeiroErro(t *testing.T) {
	var buf strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"(#132001) Template name does not exist in the translation","code":132001}}`))
	}))
	t.Cleanup(srv.Close)

	s, err := whatsapp.New(whatsapp.Config{
		PhoneNumberID: "1", Token: "t", Template: "otp_auth",
		Language: "pt_BR", Fallbacks: []string{"en", "en_US"},
		BaseURL: srv.URL, Log: logTo(&buf),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), "+258841234567", "123456", "whatsapp"); err == nil {
		t.Fatal("devia falhar")
	}
	if !strings.Contains(buf.String(), "lingua=pt_BR") {
		t.Fatalf("o registo tem de dizer a língua configurada: %s", buf.String())
	}
}
