package sms_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/airosp/airo-api/internal/platform/sms"
)

/*
 * O canal de mensagens, contra um fornecedor de mentira.
 *
 * O que interessa provar sem gastar dinheiro: que o pedido vai com a forma
 * certa, que o texto não leva ligações, e que "este número não recebe" se
 * distingue de "falhou agora".
 */

func servidorDeEnsaio(t *testing.T, status int, corpo string, ver func(*http.Request, string)) *sms.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if ver != nil {
			ver(r, r.Form.Get("Body"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	t.Cleanup(srv.Close)

	return sms.New(sms.Config{
		Provider: "twilio", AccountSID: "AC123", AuthToken: "segredo",
		From: "+258840000000", BaseURL: srv.URL,
	})
}

func TestAMensagemVaiComOCodigoESemLigacoes(t *testing.T) {
	var texto, para, de string
	var autorizacao string
	c := servidorDeEnsaio(t, 201, `{"sid":"SM123"}`, func(r *http.Request, corpo string) {
		texto, para, de = corpo, r.Form.Get("To"), r.Form.Get("From")
		autorizacao = r.Header.Get("Authorization")
	})

	id, err := c.Send(context.Background(), "+258841234567", "654321")
	if err != nil {
		t.Fatal(err)
	}
	if id != "SM123" {
		t.Errorf("identificador %q", id)
	}
	if !strings.Contains(texto, "654321") {
		t.Errorf("a mensagem não leva o código: %q", texto)
	}
	// ⚠️ Sem ligações: uma mensagem com um endereço lá dentro é a forma de
	// todas as burlas por SMS, e ensinar as pessoas a tocar nelas é ensiná-las
	// a serem enganadas.
	if strings.Contains(texto, "http") || strings.Contains(texto, "www.") {
		t.Errorf("a mensagem leva uma ligação: %q", texto)
	}
	if !strings.Contains(texto, "Não o partilhes") {
		t.Errorf("a mensagem não avisa para não partilhar: %q", texto)
	}
	if para != "+258841234567" || de != "+258840000000" {
		t.Errorf("para=%q de=%q", para, de)
	}
	if !strings.HasPrefix(autorizacao, "Basic ") {
		t.Errorf("sem credenciais no pedido")
	}
}

/*
 * "Este número não recebe" distingue-se de "falhou agora".
 *
 * A primeira não melhora com uma segunda tentativa e é o que faz o encadeado
 * parar; a segunda é nossa, e quem pediu não fez nada de errado.
 */
func TestUmNumeroQueNaoRecebeDizQueNaoRecebe(t *testing.T) {
	c := servidorDeEnsaio(t, 400, `{"code":21614,"message":"não é um número móvel"}`, nil)

	_, err := c.Send(context.Background(), "+258841234567", "654321")
	if !errors.Is(err, sms.ErrNaoEntregavel) {
		t.Fatalf("veio %v", err)
	}
	var u interface{ Undeliverable() bool }
	if !errors.As(err, &u) || !u.Undeliverable() {
		t.Errorf("o encadeado não vai saber que pode parar: %v", err)
	}
}

func TestUmaFalhaDoFornecedorNaoEDoNumero(t *testing.T) {
	c := servidorDeEnsaio(t, 500, `{"code":20500,"message":"erro interno"}`, nil)

	_, err := c.Send(context.Background(), "+258841234567", "654321")
	if err == nil {
		t.Fatal("esperava erro")
	}
	var u interface{ Undeliverable() bool }
	if errors.As(err, &u) && u.Undeliverable() {
		t.Errorf("uma falha do fornecedor ficou marcada como número inválido: %v", err)
	}
}

// Sem credenciais não se tenta: cada tentativa custa uma ao que quer entrar.
func TestSemCredenciaisNaoTenta(t *testing.T) {
	c := sms.New(sms.Config{})
	if c.Configured() {
		t.Fatal("diz estar configurado sem credenciais")
	}
	if _, err := c.Send(context.Background(), "+258841234567", "1"); err == nil {
		t.Error("tentou enviar sem credenciais")
	}
}
