// Package intelligence é o vocabulário da camada de inteligência.
//
// ⚠️ **A IA reconhece, estrutura e explica. Não decide.** Calorias, macros,
// cargas e prescrições continuam a sair dos motores em `engine/`, que ficam
// puros: nenhum deles importa este pacote, e nenhum deles fala com a rede.
//
// O fornecedor entra por uma interface. O serviço não sabe que é a OpenAI —
// não sabe como se autentica, como serializa, nem como devolve. Sabe que pediu
// uma resposta com uma forma e que recebeu bytes que tem de validar. É isso que
// deixa os testes correrem com um fornecedor falso e que deixa trocar de
// fornecedor sem tocar no domínio.
package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Provider é o fornecedor de modelos.
type Provider interface {
	// GenerateStructured pede uma resposta com a forma de `Schema`.
	GenerateStructured(ctx context.Context, req StructuredRequest) (StructuredResult, error)
	// AnalyzeImage é o mesmo pedido, com uma imagem junta.
	AnalyzeImage(ctx context.Context, req ImageRequest) (StructuredResult, error)
}

// StructuredRequest é um pedido com resposta tipada.
type StructuredRequest struct {
	// Task identifica a operação nos registos e no consumo. Nunca vai ao modelo.
	Task string
	// SchemaName e Schema são o contrato da resposta, em JSON Schema.
	SchemaName string
	Schema     json.RawMessage
	// Instructions são as regras do sistema. Fixas, escritas por nós.
	Instructions string
	// Payload é o que vem de quem usa a app — dados, **nunca instruções**.
	Payload   []byte
	MaxTokens int
}

// ImageRequest junta uma imagem ao pedido.
type ImageRequest struct {
	StructuredRequest
	Image []byte
	MIME  string
}

// StructuredResult é o que voltou, ainda por validar.
//
// ⚠️ Uma resposta conforme ao esquema é uma resposta **bem formada**, não uma
// resposta verdadeira. Quem a usa valida o domínio a seguir.
type StructuredResult struct {
	Data         []byte
	Model        string
	RequestID    string
	InputTokens  int64
	OutputTokens int64
	Latency      time.Duration
}

// Kind classifica uma falha do fornecedor pelo que se pode fazer com ela.
type Kind string

const (
	// Transient melhora com uma nova tentativa: rede, 5xx, limite de ritmo.
	Transient Kind = "transient"
	// Permanent não melhora: pedido mal feito, chave errada, modelo inexistente.
	Permanent Kind = "permanent"
	// Refused é o modelo a recusar responder.
	Refused Kind = "refused"
	// Incomplete é a resposta cortada a meio — por limite de tokens, por exemplo.
	Incomplete Kind = "incomplete"
	// Quota é a conta do fornecedor sem saldo. Não se tenta outra vez.
	Quota Kind = "quota"
)

// ProviderError é uma falha do fornecedor, já classificada.
type ProviderError struct {
	Kind       Kind
	Status     int
	RetryAfter time.Duration
	// Code é o código do fornecedor, para os registos. Nunca o corpo inteiro.
	Code string
	Msg  string
}

func (e *ProviderError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("fornecedor (%s, HTTP %d): %s", e.Kind, e.Status, e.Msg)
	}
	return fmt.Sprintf("fornecedor (%s): %s", e.Kind, e.Msg)
}

// KindOf devolve a classificação de um erro; um erro desconhecido é passageiro.
//
// Passageiro e não permanente: um erro que não se sabe ler é mais vezes a rede
// do que um pedido errado, e desistir à primeira deixava uma fotografia sem
// análise por uma falha que se resolvia sozinha.
func KindOf(err error) Kind {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return Transient
}

// Erros do serviço, antes de chegar ao fornecedor.
var (
	// ErrDisabled: a funcionalidade está desligada nesta instalação.
	ErrDisabled = errors.New("inteligência desligada")
	// ErrNoConsent: a pessoa não autorizou esta finalidade.
	ErrNoConsent = errors.New("sem consentimento para esta finalidade")
	// ErrBudgetExhausted: o orçamento do mês acabou.
	ErrBudgetExhausted = errors.New("orçamento de inteligência esgotado")
	// ErrDailyLimit: a pessoa chegou ao limite do dia.
	ErrDailyLimit = errors.New("limite diário atingido")
)
