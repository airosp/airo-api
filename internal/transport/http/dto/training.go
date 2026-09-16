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

	/*
	 * Performed é o que aconteceu em cada série — a carga incluída.
	 *
	 * ⚠️ **É o único sítio por onde a carga entra.** O plano diz `4 × 10`; o que
	 * ficou por dizer era com quantos quilos, e sem isso metade de uma app de
	 * treino — a musculação com peso — não tinha onde ser registada. O mesmo
	 * campo serve a distância, para a corrida.
	 *
	 * Opcional: um treino sem isto grava como sempre gravou, e a tabela fica
	 * sem detalhe em vez de ficar com o detalhe inventado a partir do alvo.
	 */
	Performed []PerformedExercise `json:"performed,omitempty"`
}

// PerformedExercise é o que se fez num exercício, série a série.
type PerformedExercise struct {
	// ExerciseID é o slug do catálogo, o mesmo que o pacote da sessão usa.
	ExerciseID string         `json:"exerciseId"`
	Sets       []PerformedSet `json:"sets"`
}

/*
 * PerformedSet é uma série feita.
 *
 * Os campos são todos opcionais porque a forma muda com o exercício: um
 * agachamento tem repetições e quilos, uma prancha tem segundos, uma corrida
 * tem metros. Mandar os quatro sempre dava três vazios em cada série.
 */
type PerformedSet struct {
	Index           int      `json:"index"`
	Reps            *int     `json:"reps,omitempty"`
	WeightKg        *float64 `json:"weightKg,omitempty"`
	DurationSeconds *int     `json:"durationSeconds,omitempty"`
	DistanceMeters  *int     `json:"distanceMeters,omitempty"`
	// Completed distingue a série feita da série que se começou e não acabou.
	Completed bool `json:"completed"`
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
	/*
	 * Performed é o que se fez em cada exercício, já escrito para se ler:
	 * "10 × 40 kg", "45 s", "400 m".
	 *
	 * ⚠️ Vazio nas sessões gravadas antes de haver carga, e nas que não a
	 * mandaram. Um exercício sem série nenhuma com detalhe não entra: não
	 * acrescenta nada ao que o resumo da sessão já diz.
	 */
	Performed []PerformedExerciseView `json:"performed,omitempty"`
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

// PerformedExerciseView é um exercício de uma sessão passada, com o que foi
// pedido e o que foi feito — as frases já escritas pelo servidor.
type PerformedExerciseView struct {
	ExerciseID string `json:"exerciseId"`
	Name       string `json:"name"`
	Sets       int    `json:"sets"`
	/** O que o plano pedia: "10 reps", "45 s". */
	Target string `json:"target"`
	/** O que se fez, série a série: ["10 × 40 kg", "8 × 42,5 kg"]. */
	Done []string `json:"done"`
}
