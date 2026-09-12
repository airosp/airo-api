// Package training é o Training Engine: monta a sessão do dia e transforma-a
// numa lista de passos executáveis. Puro, como os outros.
package training

type MovementPattern string

const (
	Push     MovementPattern = "push"
	Pull     MovementPattern = "pull"
	Squat    MovementPattern = "squat"
	Hinge    MovementPattern = "hinge"
	Lunge    MovementPattern = "lunge"
	Core     MovementPattern = "core"
	Cardio   MovementPattern = "cardio"
	Mobility MovementPattern = "mobility"
)

type Focus string

const (
	FocusUpper    Focus = "upper"
	FocusLower    Focus = "lower"
	FocusCardio   Focus = "cardio"
	FocusFull     Focus = "full"
	FocusMobility Focus = "mobility"
)

type Measure string

const (
	Reps Measure = "reps"
	Time Measure = "time"
)

type Experience string

const (
	Beginner     Experience = "beginner"
	Intermediate Experience = "intermediate"
	Advanced     Experience = "advanced"
)

// Role é o papel do exercício **nesta** sessão.
//
// Não se deduz das marcas do exercício: num dia de mobilidade, um alongamento
// marcado como arrefecimento é o trabalho principal. Quem monta a sessão é que
// sabe, e é ele que o diz.
type Role string

const (
	Warmup   Role = "warmup"
	Main     Role = "main"
	Cooldown Role = "cooldown"
)

type Exercise struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Muscles []string `json:"muscles"`
	// Equipment vazio = só o corpo, logo está sempre disponível. Basta ter
	// **um** dos listados.
	Equipment []string        `json:"equipment"`
	Pattern   MovementPattern `json:"pattern"`
	Measure   Measure         `json:"measure"`
	// Cue é um lembrete de execução, não instrução técnica.
	Cue       string     `json:"cue"`
	DemoQuery string     `json:"demoQuery"`
	DemoURL   string     `json:"demoUrl,omitempty"`
	Level     Experience `json:"level"`
	Warmup    bool       `json:"warmup,omitempty"`
	Cooldown  bool       `json:"cooldown,omitempty"`
}

// Prescription é o que a pessoa fixou: séries e alvo que a Airo deixa de ajustar.
type Prescription struct {
	Sets int `json:"sets"`
	// Target são repetições por série, ou segundos quando a medida é tempo.
	Target int `json:"target"`
}

type SessionExercise struct {
	Exercise    Exercise `json:"exercise"`
	Role        Role     `json:"role"`
	Sets        int      `json:"sets"`
	Target      int      `json:"target"`
	RestSeconds int      `json:"restSeconds"`
}

type Session struct {
	// ID é estável para o mesmo dia e o mesmo perfil.
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Focus            Focus             `json:"focus"`
	Summary          string            `json:"summary"`
	Muscles          []string          `json:"muscles"`
	Exercises        []SessionExercise `json:"exercises"`
	EstimatedSeconds int               `json:"estimatedSeconds"`
	EstimatedKcal    int               `json:"estimatedKcal"`
}

type StepKind string

const (
	StepGetReady StepKind = "get_ready"
	StepSet      StepKind = "set"
	StepRest     StepKind = "rest"
	StepHydrate  StepKind = "hydrate"
	StepDone     StepKind = "done"
)

type RestReason string

const (
	BetweenSets      RestReason = "between_sets"
	BetweenExercises RestReason = "between_exercises"
)

// Step é um passo da sessão.
//
// A linha do tempo calcula-se de uma vez e não à medida que a pessoa avança:
// assim o progresso, o "a seguir" e o tempo que falta saem todos da mesma
// lista, em vez de três contas que mais cedo ou mais tarde discordam.
type Step struct {
	Kind    StepKind `json:"kind"`
	Seconds int      `json:"seconds,omitempty"`

	ExerciseIndex int     `json:"exerciseIndex,omitempty"`
	Role          Role    `json:"role,omitempty"`
	SetNumber     int     `json:"setNumber,omitempty"`
	TotalSets     int     `json:"totalSets,omitempty"`
	Measure       Measure `json:"measure,omitempty"`
	Target        int     `json:"target,omitempty"`

	Reason            RestReason `json:"reason,omitempty"`
	NextExerciseIndex int        `json:"nextExerciseIndex,omitempty"`
	NextSetNumber     int        `json:"nextSetNumber,omitempty"`
}
