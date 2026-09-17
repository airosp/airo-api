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

/*
 * Impact diz quanta pancada o exercício põe nas articulações.
 *
 * Não é o mesmo que ser difícil: um agachamento com barra é pesado e não tem
 * impacto nenhum; um polichinelo é leve e aterra a cada segundo. É esta a
 * distinção que deixa treinar quem tem joelhos maus — ou vizinhos por baixo.
 */
type Impact string

const (
	ImpactoBaixo    Impact = "low"
	ImpactoModerado Impact = "moderate"
	ImpactoAlto     Impact = "high"
)

// ordemDoImpacto dá-lhes ordem, para "no máximo moderado" querer dizer alguma
// coisa.
var ordemDoImpacto = map[Impact]int{ImpactoBaixo: 0, ImpactoModerado: 1, ImpactoAlto: 2}

/*
 * PadroesDe são todos os padrões do exercício.
 *
 * Vazio na biblioteca quer dizer "só o principal": guardar `["push"]` em
 * cinquenta entradas que já dizem `pattern: push` era ruído a pedir para
 * divergir.
 */
func PadroesDe(e Exercise) []MovementPattern {
	if len(e.Patterns) > 0 {
		return e.Patterns
	}
	return []MovementPattern{e.Pattern}
}

// ImpactoDe é o impacto declarado, ou baixo — que é o caso da maioria.
func ImpactoDe(e Exercise) Impact {
	if e.Impact == "" {
		return ImpactoBaixo
	}
	return e.Impact
}

// CabeNoImpacto diz se o exercício respeita um tecto. Tecto vazio = sem tecto.
func CabeNoImpacto(e Exercise, tecto Impact) bool {
	if tecto == "" {
		return true
	}
	return ordemDoImpacto[ImpactoDe(e)] <= ordemDoImpacto[tecto]
}

/*
 * ObjectivosDe diz a que objectivos o exercício serve.
 *
 * É uma regra e não um campo: escrever os objectivos sessenta vezes à mão era
 * copiar a mesma decisão sessenta vezes, e mudá-la passava a ser sessenta
 * edições. O vocabulário é o mesmo que a pessoa escolhe no plano — `strength`,
 * `fatLoss`, `muscle`, `habit` — para a comparação ser directa.
 */
func ObjectivosDe(e Exercise) []string {
	tem := map[MovementPattern]bool{}
	for _, p := range PadroesDe(e) {
		tem[p] = true
	}

	objectivos := []string{}
	junta := func(o string) {
		for _, j := range objectivos {
			if j == o {
				return
			}
		}
		objectivos = append(objectivos, o)
	}

	// Carga externa constrói força; peso do corpo com padrão de força constrói
	// músculo a quem está a começar, e menos a quem já treina há anos.
	temForca := tem[Push] || tem[Pull] || tem[Squat] || tem[Hinge] || tem[Lunge]
	if temForca {
		if len(e.Equipment) > 0 || e.Level == Advanced {
			junta("strength")
		}
		junta("muscle")
	}
	if tem[Cardio] {
		junta("fatLoss")
	}
	// O core é estabilidade: não engorda um bíceps nem queima uma refeição, e
	// é o que sustenta tudo o resto.
	if tem[Core] && !temForca {
		junta("habit")
	}
	if tem[Mobility] {
		junta("habit")
	}
	// Tudo serve para criar o hábito, e o hábito é o objectivo de quem começa.
	junta("habit")
	return objectivos
}

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
	// Patterns são todos os padrões que o exercício atravessa, quando é mais
	// do que um. Um burpee é agachamento, empurrar e cardio ao mesmo tempo, e
	// guardar só um obrigava a escolher qual — com a escolha errada para
	// metade das perguntas. Vazio = só o `Pattern`; ver `PadroesDe`.
	Patterns []MovementPattern `json:"patterns,omitempty"`
	// Impact é a força com que o corpo volta ao chão. Vazio = baixo, que é a
	// maioria; ver `ImpactoDe`.
	Impact  Impact  `json:"impact,omitempty"`
	Measure Measure `json:"measure"`
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
