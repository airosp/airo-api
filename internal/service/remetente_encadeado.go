package service

import (
	"context"
	"errors"
	"log/slog"
)

/*
 * O remetente encadeado: WhatsApp primeiro, mensagem depois.
 *
 * ⚠️ Trinta segundos sem entrega e não havia plano B. Quem não tem WhatsApp
 * activo ficava à porta da app — e em Moçambique isso não é um caso de canto.
 *
 * A passagem para o segundo canal só acontece quando o primeiro diz
 * **"este número não recebe por aqui"** (`Undeliverable`), e não quando falha
 * por outra razão. A distinção é o que impede duas mensagens pagas por cada
 * pedido sempre que a Meta tiver um mau minuto.
 *
 * O canal pedido manda sobre a ordem: quem escolhe "sms" no ecrã recebe SMS, e
 * não um WhatsApp que não vai ler.
 */
type RemetenteEncadeado struct {
	Principal   Sender
	Alternativo Sender
	Log         *slog.Logger
}

/*
 * Send tenta o principal e, se ele disser que o número não serve, tenta o
 * alternativo.
 *
 * O identificador devolvido é o do canal que aceitou — é ele que o webhook de
 * entrega vai referir, e trocá-los faria a confirmação não bater com nada.
 */
func (r RemetenteEncadeado) Send(ctx context.Context, telefone, codigo, canal string) (string, error) {
	primeiro, segundo := r.Principal, r.Alternativo

	// Quem pediu SMS recebe SMS. Não é uma preferência estética: é quem sabe
	// que não tem WhatsApp neste telemóvel.
	if canal == "sms" && r.Alternativo != nil {
		primeiro, segundo = r.Alternativo, r.Principal
	}
	if primeiro == nil {
		primeiro, segundo = segundo, nil
	}
	if primeiro == nil {
		return "", errors.New("sem canal de envio configurado")
	}

	id, err := primeiro.Send(ctx, telefone, codigo, canal)
	if err == nil {
		return id, nil
	}

	var naoEntregavel Undeliverable
	if segundo == nil || !errors.As(err, &naoEntregavel) || !naoEntregavel.Undeliverable() {
		// Ou não há para onde ir, ou a falha não é do número: devolve-se como
		// está. Tentar o segundo canal por causa de uma falha de rede seria
		// pagar duas mensagens de cada vez que a Meta espirra.
		return "", err
	}

	if r.Log != nil {
		// Sem o número em claro e sem o código: só que houve passagem, para se
		// poder contar quantas.
		r.Log.Info("canal alternativo", "razao", "numero_nao_recebe_no_principal")
	}
	return segundo.Send(ctx, telefone, codigo, canal)
}

/*
 * RemetenteSMS liga um cliente de mensagens à interface que o serviço conhece.
 *
 * O `canal` não é usado: uma mensagem é uma mensagem. Fica no parâmetro porque
 * é a interface, e mudá-la só para este caso era mudá-la para todos.
 */
type RemetenteSMS struct {
	Enviar func(ctx context.Context, telefone, codigo string) (string, error)
}

func (s RemetenteSMS) Send(ctx context.Context, telefone, codigo, _ string) (string, error) {
	return s.Enviar(ctx, telefone, codigo)
}
