package apierr

import (
	"context"
	"net/http"
	"sync"
)

// A causa de um 500.
//
// Existe porque um 500 que não regista porquê não deixa nada para investigar:
// o cliente recebe "não foi possível", o registo escreve `status=500`, e o erro
// verdadeiro — a coluna que falta, a ligação que caiu — desaparece.
//
// O handler não recebe um logger para isto. Guarda a causa no pedido, e o
// middleware de registo, que já escreve a linha, escreve-a com o motivo. Um
// ponteiro partilhado porque o contexto de um handler não sobe até quem o
// chamou: o valor tem de ser o mesmo objecto, não uma cópia.
type cause struct {
	mu  sync.Mutex
	err error
}

type causeKey struct{}

// WithCause prepara o pedido para poder carregar a causa de uma falha.
// Chamado uma vez, no middleware, antes de tudo o resto.
func WithCause(ctx context.Context) context.Context {
	return context.WithValue(ctx, causeKey{}, &cause{})
}

// Fail regista o erro que levou a esta resposta. Não escreve nada ao cliente —
// o que ele vê continua a ser a mensagem que o handler escolheu.
func Fail(r *http.Request, err error) {
	if err == nil {
		return
	}
	if c, ok := r.Context().Value(causeKey{}).(*cause); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.err == nil {
			// A primeira causa é a que interessa: as seguintes são
			// consequências dela.
			c.err = err
		}
	}
}

// CauseFrom devolve a causa guardada, ou nil.
func CauseFrom(ctx context.Context) error {
	if c, ok := ctx.Value(causeKey{}).(*cause); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.err
	}
	return nil
}

// WriteInternal responde 500 e guarda o motivo para o registo.
//
// É o par de `Write`: sempre que a resposta é `Internal`, a causa tem de ir
// para algum lado — senão fica só o número.
func WriteInternal(w http.ResponseWriter, r *http.Request, err error, message string) {
	Fail(r, err)
	Write(w, Internal, message, "")
}
