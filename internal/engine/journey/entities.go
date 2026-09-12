package journey

// Tipos partilhados pelos motores da jornada. Serializáveis: a mesma avaliação
// atravessa `GET /v1/progress/snapshot` sem mudar de forma.

type Confidence string

const (
	Low    Confidence = "low"
	Medium Confidence = "medium"
	High   Confidence = "high"
)

type Level string

const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
)

type Status string

const (
	Draft     Status = "draft"
	Active    Status = "active"
	Paused    Status = "paused"
	Completed Status = "completed"
	Abandoned Status = "abandoned"
	Archived  Status = "archived"
)

type Journey struct {
	ID            string
	GoalID        string
	StartDateISO  string
	TargetDateISO string // vazio = horizonte aberto
}

type Phase struct {
	ID           string    `json:"id"`
	JourneyID    string    `json:"journeyId"`
	Kind         PhaseKind `json:"kind"`
	Index        int       `json:"index"`
	Title        string    `json:"title"`
	Intent       string    `json:"intent"`
	StartDateISO string    `json:"startDateISO"`
	EndDateISO   string    `json:"endDateISO"`
	Weeks        int       `json:"weeks"`
	Status       Status    `json:"status"`
}

// SessionRecord é o facto: o que aconteceu, não o que estava planeado.
type SessionRecord struct {
	OccurredAtISO string
	// Status distingue quem abriu e desistiu de quem nunca apareceu. Uma sessão
	// saltada grava na mesma — é a única prova da segunda.
	Status                 string // completed | skipped
	PlannedDurationMinutes int
	ActualDurationMinutes  *int
}

type Plan struct {
	Frequency              int
	SessionDurationMinutes int
	// TrainingDays: 0 = segunda … 6 = domingo.
	TrainingDays []int
}

type Adherence struct {
	Frequency  float64 `json:"frequency"`
	Duration   float64 `json:"duration"`
	Completion float64 `json:"completion"`
	Schedule   float64 `json:"schedule"`
	Score      float64 `json:"score"`

	PlannedSessions   int `json:"plannedSessions"`
	CompletedSessions int `json:"completedSessions"`
	SkippedSessions   int `json:"skippedSessions"`

	// Evaluable é falso enquanto não tiver havido sessões planeadas. Nada a
	// julgar — e julgar mesmo assim daria 0% a quem começou ontem.
	Evaluable bool `json:"evaluable"`
}

type Trend struct {
	RollingAverage  float64
	PreviousAverage *float64
	RatePerWeek     *float64
	Samples         int
	SpanDays        int
	Confidence      Confidence
}

type RiskType string

const (
	LowAdherence RiskType = "low_adherence"
	RapidChange  RiskType = "rapid_change"
	Plateau      RiskType = "plateau"
	VolumeSpike  RiskType = "volume_spike"
	StalledStart RiskType = "stalled_start"
	NoResponse   RiskType = "no_response"
)

type Risk struct {
	ID         string     `json:"id"`
	Type       RiskType   `json:"type"`
	Level      Level      `json:"level"`
	Confidence Confidence `json:"confidence"`
	// Evidence são os factos que sustentam a decisão. Sem eles não há como
	// explicar à pessoa porque é que o plano mudou.
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
}

type AdaptationKind string

const (
	Maintain        AdaptationKind = "maintain"
	ReduceLoad      AdaptationKind = "reduce_load"
	IncreaseLoad    AdaptationKind = "increase_load"
	Simplify        AdaptationKind = "simplify"
	ExtendTimeframe AdaptationKind = "extend_timeframe"
	ReviewGoal      AdaptationKind = "review_goal"
)

type PlanChanges struct {
	Frequency              *int       `json:"frequency,omitempty"`
	SessionDurationMinutes *int       `json:"sessionDurationMinutes,omitempty"`
	Intensity              *Intensity `json:"intensity,omitempty"`
	RecoveryStrategy       *Recovery  `json:"recoveryStrategy,omitempty"`
}

type AdaptationDecision struct {
	ID           string         `json:"id"`
	JourneyID    string         `json:"journeyId"`
	CreatedAtISO string         `json:"createdAtISO"`
	Kind         AdaptationKind `json:"kind"`
	Reason       string         `json:"reason"`
	Changes      PlanChanges    `json:"changes"`
	// Applied é falso enquanto mexer no que o utilizador escolheu. A Airo
	// propõe; quem aplica é ele.
	Applied bool `json:"applied"`
}
