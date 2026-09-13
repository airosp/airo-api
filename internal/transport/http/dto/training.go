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

	// ClassID, quando o treino foi uma aula gravada.
	//
	// ⚠️ O cliente **não** manda o tempo planeado numa aula: esse é a duração
	// do vídeo, e é o servidor que a lê. Deixá-lo mandar era deixá-lo dizer que
	// uma aula de 38 minutos pedia 5.
	ClassID *string `json:"classId,omitempty"`
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

// SessionEventsRequest é o lote de factos de uma sessão.
//
// ⚠️ **Sem `status`.** Quem decide se a sessão conta é o servidor. O cliente diz
// o que aconteceu e quando.
type SessionEventsRequest struct {
	// LocalDay é o dia **do utilizador**: um treino à uma da manhã em Maputo é
	// dia anterior em UTC.
	LocalDay string         `json:"localDay"`
	Events   []SessionEvent `json:"events"`
}

type SessionEvent struct {
	At   string `json:"at"`
	Type string `json:"type"`
	// Index é o passo da linha do tempo a que o evento pertence, quando faz
	// sentido — um `session_ended` não tem passo.
	Index   *int           `json:"index,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

type SessionEventsResponse struct {
	Accepted int `json:"accepted"`
	// Duplicates são os que já lá estavam. Reenviar é normal, não é erro: é o
	// que acontece quando a rede cai a meio do envio.
	Duplicates int  `json:"duplicates"`
	Ended      bool `json:"ended"`
	// Preenchidos só quando o `session_ended` chegou.
	Status          string `json:"status,omitempty"`
	CountsForStreak bool   `json:"countsForStreak,omitempty"`
	Streak          int    `json:"streak,omitempty"`
}
