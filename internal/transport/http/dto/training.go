package dto

import "github.com/airosp/airo-api/internal/transport/http/view"

// TodayResponse é o pacote da sessão, tal como o cliente o percorre.
//
// Reexportado do `view` de propósito: é o `view` que decide o que cada passo
// mostra, e o `dto` só diz que é isso que sai na resposta. Duplicar os tipos
// abriria a porta a divergirem.
type TodayResponse = view.SessionPackage

// RecordSessionRequest é o que o cliente envia ao fim de um treino.
//
// ⚠️ **Sem `status`.** Quem decide se conta como feita é o servidor, aplicando
// o limiar. Deixar o cliente mandar `"completed"` devolvia-lhe a regra pela
// porta das traseiras — e é a regra que distingue treinar de folhear o ecrã.
type RecordSessionRequest struct {
	// OccurredAt é quando a sessão terminou.
	OccurredAt string `json:"occurredAt"`
	// LocalDay é o dia **do utilizador**, e não `date(occurredAt)`: um treino à
	// uma da manhã em Maputo é dia anterior em UTC, e contá-lo em UTC partia a
	// sequência a quem treina de madrugada.
	LocalDay string `json:"localDay"`

	PlannedSeconds  int `json:"plannedSeconds"`
	DurationSeconds int `json:"durationSeconds"`

	SetsDone int `json:"setsDone"`

	Blocks *BlocksRequest `json:"blocks,omitempty"`
}

type BlocksRequest struct {
	WarmupSeconds   *int `json:"warmupSeconds"`
	MainSeconds     *int `json:"mainSeconds"`
	CooldownSeconds *int `json:"cooldownSeconds"`
}

// RecordSessionResponse traz o **veredicto**.
type RecordSessionResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// CountsForStreak é a decisão, não o dado: o cliente desenha a sequência que
	// lhe mandam, em vez de a calcular.
	CountsForStreak bool `json:"countsForStreak"`
	Streak          int  `json:"streak"`
}

// SessionHistoryItem é uma sessão como o histórico a mostra.
//
// `id` é o identificador do telemóvel quando existe — a chave de idempotência
// com que a sessão foi gravada. É o que permite ao aparelho reconhecer o que já
// é seu em vez de duplicar o histórico ao voltar a entrar.
type SessionHistoryItem struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Focus           string `json:"focus"`
	Status          string `json:"status"`
	OccurredAt      string `json:"occurredAt"`
	LocalDay        string `json:"localDay"`
	PlannedSeconds  int    `json:"plannedSeconds"`
	DurationSeconds int    `json:"durationSeconds"`
	SetsPlanned     int    `json:"setsPlanned"`
	SetsDone        int    `json:"setsDone"`
	Kcal            int    `json:"kcal"`
	Exercises       int    `json:"exercises"`
	// Blocks é nil nas sessões gravadas antes de o campo existir — e dizê-lo
	// com ausência é mais honesto do que inventar três zeros.
	Blocks *SessionBlocks `json:"blocks,omitempty"`
}

type SessionBlocks struct {
	WarmupSeconds   int `json:"warmupSeconds"`
	MainSeconds     int `json:"mainSeconds"`
	CooldownSeconds int `json:"cooldownSeconds"`
}

type SessionHistoryResponse struct {
	Sessions []SessionHistoryItem `json:"sessions"`
}
