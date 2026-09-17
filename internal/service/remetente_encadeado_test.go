package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/airosp/airo-api/internal/service"
)

/*
 * O segundo canal, para quem não tem WhatsApp.
 *
 * ⚠️ Trinta segundos sem entrega e não havia plano B: quem não tem WhatsApp
 * activo ficava à porta da app. Em Moçambique isso não é um caso de canto.
 */

type remetenteDeEnsaio struct {
	id      string
	erro    error
	chamado int
	canais  []string
}

func (r *remetenteDeEnsaio) Send(_ context.Context, _, _, canal string) (string, error) {
	r.chamado++
	r.canais = append(r.canais, canal)
	if r.erro != nil {
		return "", r.erro
	}
	return r.id, nil
}

type naoRecebe struct{ error }

func (naoRecebe) Undeliverable() bool { return true }

func TestQuemNaoTemWhatsAppRecebeMensagem(t *testing.T) {
	whats := &remetenteDeEnsaio{erro: naoRecebe{errors.New("sem whatsapp neste número")}}
	mensagem := &remetenteDeEnsaio{id: "sms-1"}

	enc := service.RemetenteEncadeado{Principal: whats, Alternativo: mensagem}
	id, err := enc.Send(context.Background(), "+258841110000", "123456", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if id != "sms-1" {
		t.Errorf("o identificador devolvido foi %q — tem de ser o do canal que aceitou", id)
	}
	if whats.chamado != 1 || mensagem.chamado != 1 {
		t.Errorf("whatsapp %d, mensagem %d", whats.chamado, mensagem.chamado)
	}
}

/*
 * Uma falha que **não** é do número não passa ao segundo canal.
 *
 * Sem esta distinção, cada mau minuto da Meta custava duas mensagens pagas por
 * cada pedido — e a fatura não avisa antes de chegar.
 */
func TestUmaFalhaDeRedeNaoGastaOSegundoCanal(t *testing.T) {
	whats := &remetenteDeEnsaio{erro: errors.New("ligação recusada")}
	mensagem := &remetenteDeEnsaio{id: "sms-1"}

	enc := service.RemetenteEncadeado{Principal: whats, Alternativo: mensagem}
	if _, err := enc.Send(context.Background(), "+258841110000", "123456", "auto"); err == nil {
		t.Fatal("devia ter devolvido o erro do primeiro canal")
	}
	if mensagem.chamado != 0 {
		t.Errorf("gastou uma mensagem por uma falha de rede")
	}
}

/*
 * Quem pede SMS recebe SMS.
 *
 * Não é uma preferência estética: é quem sabe que não tem WhatsApp neste
 * telemóvel, e mandar-lhe um WhatsApp é mandá-lo esperar por nada.
 */
func TestQuemPedeMensagemRecebeMensagemPrimeiro(t *testing.T) {
	whats := &remetenteDeEnsaio{id: "wa-1"}
	mensagem := &remetenteDeEnsaio{id: "sms-1"}

	enc := service.RemetenteEncadeado{Principal: whats, Alternativo: mensagem}
	id, err := enc.Send(context.Background(), "+258841110000", "123456", "sms")
	if err != nil {
		t.Fatal(err)
	}
	if id != "sms-1" {
		t.Errorf("veio %q", id)
	}
	if whats.chamado != 0 {
		t.Errorf("mandou WhatsApp a quem pediu mensagem")
	}
}

// Sem segundo canal, o primeiro continua a ser o que há — e o erro é o dele.
func TestSemSegundoCanalNadaMuda(t *testing.T) {
	whats := &remetenteDeEnsaio{erro: naoRecebe{errors.New("sem whatsapp")}}
	enc := service.RemetenteEncadeado{Principal: whats}

	_, err := enc.Send(context.Background(), "+258841110000", "123456", "auto")
	var u service.Undeliverable
	if !errors.As(err, &u) {
		t.Fatalf("o erro perdeu-se pelo caminho: %v", err)
	}
}

// E se só houver o segundo, é por ele que se manda.
func TestSoComMensagemMandaPorMensagem(t *testing.T) {
	mensagem := &remetenteDeEnsaio{id: "sms-1"}
	enc := service.RemetenteEncadeado{Alternativo: mensagem}

	id, err := enc.Send(context.Background(), "+258841110000", "123456", "auto")
	if err != nil || id != "sms-1" {
		t.Fatalf("%q — %v", id, err)
	}
}
