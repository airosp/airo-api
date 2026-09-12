package goal

// Vocabulário do Goal Engine.
//
// Tudo serializável de propósito: a mesma avaliação tem de atravessar
// `POST /v1/goals/{id}/assess` sem mudar de forma. Por isso as recomendações
// são **dados**, não funções.

type Direction string

const (
	LoseWeight     Direction = "lose_weight"
	GainWeight     Direction = "gain_weight"
	MaintainWeight Direction = "maintain_weight"
)

type Priority string

const (
	PriorityWeight      Priority = "weight"
	PriorityMuscle      Priority = "muscle"
	PriorityPerformance Priority = "performance"
	PriorityHealth      Priority = "health"
	PriorityAppearance  Priority = "appearance"
)

type Status string

const (
	StatusExcellent   Status = "excellent"
	StatusRealistic   Status = "realistic"
	StatusAmbitious   Status = "ambitious"
	StatusAggressive  Status = "aggressive"
	StatusNeedsReview Status = "needs_review"
)

type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

type Category string

const (
	CatBody      Category = "body"
	CatRate      Category = "rate"
	CatTraining  Category = "training"
	CatRecovery  Category = "recovery"
	CatNutrition Category = "nutrition"
	CatSafety    Category = "safety"
)

type Confidence string

const (
	Low    Confidence = "low"
	Medium Confidence = "medium"
	High   Confidence = "high"
)

// RangePosition é a posição face ao intervalo de referência de IMC.
// Contexto, não diagnóstico.
type RangePosition string

const (
	InReference    RangePosition = "reference"
	BelowReference RangePosition = "below_reference"
	AboveReference RangePosition = "above_reference"
)

type Adequacy string

const (
	Sufficient   Adequacy = "sufficient"
	Limited      Adequacy = "limited"
	Insufficient Adequacy = "insufficient"
)

type Trend string

const (
	Ahead   Trend = "ahead"
	OnTrack Trend = "on_track"
	Behind  Trend = "behind"
	Unknown Trend = "unknown"
)

type Sex string

const (
	Male        Sex = "male"
	Female      Sex = "female"
	Other       Sex = "other"
	Unspecified Sex = "unspecified"
)

// ── Entrada ──────────────────────────────────────────────────────────────────

type Body struct {
	CurrentWeightKg float64
	HeightCm        *float64
	Age             *int
	// Sex é opcional por princípio: o motor funciona sem, com menos confiança.
	Sex               *Sex
	WaistCm           *float64
	BodyFatPercentage *float64
}

type GoalInput struct {
	TargetWeightKg *float64
	// TargetDateISO vazio quer dizer sem prazo. O motor estima um intervalo em
	// vez de inventar uma data.
	TargetDateISO string
	Priority      *Priority
}

type Training struct {
	DaysPerWeek     int
	DurationMinutes int
	Experience      string
	Equipment       []string
}

type WeightPoint struct {
	DateISO  string
	WeightKg float64
}

type Input struct {
	Body     Body
	Goal     GoalInput
	Training Training
	History  []WeightPoint
	// NowISO é injectável para os cálculos serem determinísticos.
	NowISO string
}

// ── Métricas ─────────────────────────────────────────────────────────────────

type WeightRange struct {
	MinKg float64 `json:"minKg"`
	MaxKg float64 `json:"maxKg"`
}

type EnergyRange struct {
	Low      int `json:"low"`
	High     int `json:"high"`
	Estimate int `json:"estimate"`
}

type BodyAssessment struct {
	CurrentBmi     *float64       `json:"currentBmi"`
	TargetBmi      *float64       `json:"targetBmi"`
	ReferenceRange *WeightRange   `json:"referenceRangeKg"`
	Current        *RangePosition `json:"current"`
	Target         *RangePosition `json:"target"`
	Confidence     Confidence     `json:"confidence"`
}

type EnergyAssessment struct {
	BmrKcal        *EnergyRange `json:"bmrKcal"`
	TdeeKcal       *EnergyRange `json:"tdeeKcal"`
	ActivityFactor float64      `json:"activityFactor"`
	// Equivalente energético da mudança pedida. Aproximação, não prescrição.
	TotalKcalEquivalent *int `json:"totalKcalEquivalent"`
	DailyKcalEquivalent *int `json:"dailyKcalEquivalent"`
}

type TrainingAssessment struct {
	DaysPerWeek       int      `json:"daysPerWeek"`
	MinutesPerSession int      `json:"minutesPerSession"`
	WeeklyMinutes     int      `json:"weeklyMinutes"`
	MonthlyMinutes    int      `json:"monthlyMinutes"`
	Adequacy          Adequacy `json:"adequacy"`
}

type WeeksRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type TimeframeAssessment struct {
	Weeks                     *int        `json:"weeks"`
	RequiredWeeklyChangeKg    *float64    `json:"requiredWeeklyChangeKg"`
	RequiredWeeklyChangeRatio *float64    `json:"requiredWeeklyChangeRatio"`
	EstimatedWeeks            *WeeksRange `json:"estimatedWeeks"`
}

type ProgressAssessment struct {
	WeeksElapsed     int     `json:"weeksElapsed"`
	StartWeightKg    float64 `json:"startWeightKg"`
	LatestWeightKg   float64 `json:"latestWeightKg"`
	ExpectedWeightKg float64 `json:"expectedWeightKg"`
	DeviationKg      float64 `json:"deviationKg"`
	Trend            Trend   `json:"trend"`
}

type Metrics struct {
	Direction       Direction           `json:"direction"`
	CurrentWeightKg float64             `json:"currentWeightKg"`
	TargetWeightKg  float64             `json:"targetWeightKg"`
	DeltaKg         float64             `json:"deltaKg"`
	DeltaRatio      float64             `json:"deltaRatio"`
	Body            BodyAssessment      `json:"body"`
	Energy          EnergyAssessment    `json:"energy"`
	Training        TrainingAssessment  `json:"training"`
	Timeframe       TimeframeAssessment `json:"timeframe"`
	Progress        *ProgressAssessment `json:"progress"`
}

// ── Saída ────────────────────────────────────────────────────────────────────

type Signal struct {
	ID       string   `json:"id"`
	Category Category `json:"category"`
	Severity Severity `json:"severity"`
	// Message é uma frase curta para o utilizador.
	Message           string   `json:"message"`
	RecommendationIDs []string `json:"-"`
}

type ActionKind string

const (
	ActionSetTarget         ActionKind = "set_target"
	ActionSetDirection      ActionKind = "set_direction"
	ActionAddTrainingDay    ActionKind = "add_training_day"
	ActionIncreaseDuration  ActionKind = "increase_duration"
	ActionExtendTimeframe   ActionKind = "extend_timeframe"
	ActionConsultSpecialist ActionKind = "consult_specialist"
	ActionKeepGoal          ActionKind = "keep_goal"
)

type Action struct {
	Kind           ActionKind `json:"kind"`
	TargetWeightKg *float64   `json:"targetWeightKg,omitempty"`
	Direction      *Direction `json:"direction,omitempty"`
	Role           string     `json:"role,omitempty"`
}

type Recommendation struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Priority string `json:"priority"`
	Action   Action `json:"action"`
}

type Milestone struct {
	Index    int     `json:"index"`
	WeightKg float64 `json:"weightKg"`
	// Progress é a fracção do caminho total, de 0 a 1.
	Progress float64 `json:"progress"`
	IsTarget bool    `json:"isTarget"`
}

// Scores é **interno**.
//
// `goalEngineConfig.scoring` diz: "Nunca são mostrados ao utilizador". Serve
// para escolher o tom da mensagem e para depurar. A camada de transporte não o
// põe em nenhum DTO — expô-lo é convidar a interface a desenhá-lo.
type Scores struct {
	Body        int `json:"body"`
	Rate        int `json:"rate"`
	Training    int `json:"training"`
	Consistency int `json:"consistency"`
	Overall     int `json:"overall"`
}

type Assessment struct {
	Status          Status           `json:"status"`
	Metrics         Metrics          `json:"metrics"`
	Signals         []Signal         `json:"signals"`
	Recommendations []Recommendation `json:"recommendations"`
	Milestones      []Milestone      `json:"milestones"`
	Scores          Scores           `json:"-"`
}
