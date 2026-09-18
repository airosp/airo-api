package view

import "encoding/json"

/*
 * A jornada como o telemóvel a desenha.
 *
 * ⚠️ O telemóvel montava-a com `startJourney`, no `AiroContext` — fases,
 * planos e acontecimentos, tudo decidido lá. O servidor já os escrevia todos
 * quando o objectivo nascia, e nunca os devolvia. Duas contas do mesmo número
 * acabavam com duas jornadas diferentes.
 */

type Journey struct {
	Goal    JourneyGoalView    `json:"goal"`
	Journey JourneyRecordView  `json:"journey"`
	Targets []JourneyTarget    `json:"targets"`
	Phases  []JourneyPhase     `json:"phases"`
	Plans   []JourneyPlan      `json:"plans"`
	Events  []JourneyEventView `json:"events"`
}

type JourneyGoalView struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Horizon   string `json:"horizon"`
	Direction string `json:"direction"`
	Priority  string `json:"priority"`
	Status    string `json:"status"`
}

type JourneyRecordView struct {
	ID            string `json:"id"`
	StartDateISO  string `json:"startDateISO"`
	TargetDateISO string `json:"targetDateISO,omitempty"`
	Horizon       string `json:"horizon"`
	Status        string `json:"status"`
	/** O índice da fase em curso. Ausente num horizonte sem data de fim. */
	CurrentPhaseIndex *int `json:"currentPhaseIndex,omitempty"`
	/** Em pausa desde quando. Ausente quando não está. */
	PausedSinceISO string `json:"pausedSinceISO,omitempty"`
}

type JourneyTarget struct {
	Metric    string  `json:"metric"`
	Direction string  `json:"direction"`
	Baseline  float64 `json:"baseline"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	DueDate   string  `json:"dueDate,omitempty"`
}

type JourneyPhase struct {
	/** É por ele que um plano diz a que fase pertence. */
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Index int    `json:"index"`
	/** O texto da fase, decidido aqui: a tabela guarda só o género e as datas. */
	Title        string `json:"title"`
	Intent       string `json:"intent"`
	StartDateISO string `json:"startDateISO"`
	EndDateISO   string `json:"endDateISO"`
	Weeks        int    `json:"weeks"`
}

type JourneyPlan struct {
	ID string `json:"id"`
	/** A fase a que pertence. Vazia num plano sem fase. */
	PhaseID          string `json:"phaseId,omitempty"`
	FrequencyPerWeek int    `json:"frequencyPerWeek"`
	SessionMinutes   int    `json:"sessionMinutes"`
	Intensity        string `json:"intensity"`
	Progression      string `json:"progression"`
	Recovery         string `json:"recovery"`
	EffectiveFromISO string `json:"effectiveFromISO"`
}

type JourneyEventView struct {
	Kind          string          `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAtISO string          `json:"occurredAtISO"`
}
