// Package view monta as respostas **já decididas** que o cliente desenha.
//
// É aqui que passa a fronteira de docs/backend/07-fronteira-cliente-servidor.md:
// o cliente recebe rótulos, percentagens e estados de botão, e nunca os deriva.
// Se um ecrã precisa de um número que não esteja numa resposta, é o servidor que
// está incompleto — não o cliente que o deve calcular.
package view

import (
	"fmt"
	"strings"

	"github.com/airosp/airo-api/internal/engine/training"
)

// ── O pacote da sessão ───────────────────────────────────────────────────────
//
// O problema que resolve: se o cliente não calcula nada, cada toque precisaria
// do servidor — e o Modo Foco tem um toque a cada poucos segundos, muitas vezes
// num ginásio sem rede.
//
// A resposta é enviar a sessão **inteiramente pré-decidida**, passo a passo. O
// cliente percorre a lista; avançar é `index++`. Resolve as duas coisas ao mesmo
// tempo: avançar é instantâneo, e o pacote é auto-suficiente do princípio ao fim.

type Dial struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
}

type Action struct {
	Label string `json:"label"`
	Icon  string `json:"icon"`
}

type SetProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type BlockProgress struct {
	Role string `json:"role"`
	// Weight é a fatia da sessão que este bloco ocupa, 0–1.
	Weight float64 `json:"weight"`
	// Filled é quanto desse bloco já passou, 0–1.
	Filled float64 `json:"filled"`
}

type Progress struct {
	Sets   SetProgress     `json:"sets"`
	Blocks []BlockProgress `json:"blocks"`
}

// Control é um botão com o seu estado **já resolvido**.
//
// A guarda — só se pode tirar uma série à frente da posição actual — é uma
// regra. O cliente não a avalia: desenha o menos apagado porque o servidor disse
// `enabled: false`, e não porque comparou `setsAhead >= 1`.
type Control struct {
	Enabled bool `json:"enabled"`
	// Reason diz porquê quando está desligado. Serve para a interface poder
	// explicar em vez de só apagar.
	Reason string `json:"reason,omitempty"`
}

type Controls struct {
	AddSet    Control `json:"addSet"`
	RemoveSet Control `json:"removeSet"`
}

type Step struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`

	Dial    Dial     `json:"dial"`
	Pill    string   `json:"pill,omitempty"`
	Capsule string   `json:"capsule,omitempty"`
	Cue     string   `json:"cue,omitempty"`
	Muscles []string `json:"muscles,omitempty"`

	Action   Action   `json:"action"`
	Progress Progress `json:"progress"`
	Controls Controls `json:"controls"`

	// AutoAdvanceAfterSeconds é nil nos passos que esperam pela pessoa.
	//
	// Uma série a repetições não tem relógio a correr contra ela: conta para
	// cima e espera. Só o que é medido a tempo avança sozinho.
	AutoAdvanceAfterSeconds *int `json:"autoAdvanceAfterSeconds"`

	// Tone é a cor do passo, decidida aqui. A hidratação é azul; o resto é a
	// cor da marca.
	Tone string `json:"tone,omitempty"`
}

type SessionPackage struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`

	EstimatedSeconds int `json:"estimatedSeconds"`
	EstimatedKcal    int `json:"estimatedKcal"`
	TotalSets        int `json:"totalSets"`

	Steps []Step `json:"steps"`

	// CompletionThresholdSeconds é o tempo abaixo do qual a sessão **não** conta
	// como feita. Vai no pacote para a interface poder explicar a decisão — mas
	// quem a toma é o servidor, ao receber os eventos.
	CompletionThresholdSeconds int `json:"completionThresholdSeconds"`

	/*
	 * O treino como lista de exercícios, e não só como linha do tempo.
	 *
	 * ⚠️ O pacote levava os **passos** — o que o Modo Foco percorre — e mais
	 * nada. Os outros três ecrãs que mostram o treino (o separador de início, o
	 * do plano e o do resumo) precisam da lista, e por isso chamavam
	 * `buildSession` **outra vez**, no telemóvel, com a mesma entrada. A
	 * sessão já estava construída aqui e era deitada fora no handler:
	 * `pkg, _, _, err`.
	 *
	 * Enquanto assim foi, duas versões da app podiam propor treinos diferentes
	 * para o mesmo dia à mesma conta — e nada no sistema dava por isso.
	 */
	Plan SessionPlan `json:"plan"`
}

/*
 * SessionPlan é o que os ecrãs desenham quando não estão a percorrer a sessão.
 *
 * Tudo aqui é **decidido**: o orçamento de minutos, se o dia é de descanso, o
 * rótulo do foco, a frase do método do treinador. Eram quatro chamadas ao motor
 * espalhadas por quatro ecrãs, cada uma a repetir a mesma conta.
 */
type SessionPlan struct {
	Focus      string   `json:"focus"`
	FocusLabel string   `json:"focusLabel"`
	Muscles    []string `json:"muscles"`
	/** O tempo que este dia do plano pede. Era `planDayMinutes` no telemóvel. */
	BudgetMinutes int `json:"budgetMinutes"`
	/** Um dia de descanso não tem treino para propor, e o ecrã diz outra coisa. */
	IsRecovery bool `json:"isRecovery"`
	/** Só as séries do trabalho principal: o aquecimento não é trabalho. */
	MainSets int `json:"mainSets"`
	/** O que o método do treinador escolhido muda. Vazio quando não há. */
	MethodPhrase string                `json:"methodPhrase,omitempty"`
	Exercises    []SessionExerciseView `json:"exercises"`
}

type SessionExerciseView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Sets        int    `json:"sets"`
	Target      int    `json:"target"`
	RestSeconds int    `json:"restSeconds"`
	Measure     string `json:"measure"`
	Pattern     string `json:"pattern"`
	/** O padrão em português. Era uma tabela repetida no telemóvel. */
	PatternLabel string   `json:"patternLabel"`
	Equipment    []string `json:"equipment"`
	Muscles      []string `json:"muscles"`
	Impact       string   `json:"impact,omitempty"`
	Cue          string   `json:"cue,omitempty"`
	DemoQuery    string   `json:"demoQuery,omitempty"`
	DemoURL      string   `json:"demoUrl,omitempty"`
}

/*
 * Os rótulos, aqui e não no telemóvel.
 *
 * São a mesma tabela que vivia em `modules/workout-engine/domain/config.ts`.
 * Uma tabela de tradução em dois sítios é uma tabela que diverge: basta alguém
 * acrescentar um padrão de movimento de um lado.
 */
var focusLabels = map[training.Focus]string{
	training.FocusUpper: "Tronco", training.FocusLower: "Pernas",
	training.FocusCardio: "Cardio", training.FocusFull: "Corpo inteiro",
	training.FocusMobility: "Mobilidade",
}

var patternLabels = map[training.MovementPattern]string{
	"push": "Empurrar", "pull": "Puxar", "squat": "Agachar", "hinge": "Anca",
	"lunge": "Afundo", "core": "Core", "cardio": "Cardio", "mobility": "Mobilidade",
}

func buildSessionPlan(c training.Config, s training.Session, planLabel, specialistID string, budgetMinutes int) SessionPlan {
	exercicios := make([]SessionExerciseView, 0, len(s.Exercises))
	principais := 0
	for _, e := range s.Exercises {
		if e.Role == training.Main {
			principais += e.Sets
		}
		exercicios = append(exercicios, SessionExerciseView{
			ID: e.Exercise.ID, Name: e.Exercise.Name, Role: string(e.Role),
			Sets: e.Sets, Target: e.Target, RestSeconds: e.RestSeconds,
			Measure:      string(e.Exercise.Measure),
			Pattern:      string(e.Exercise.Pattern),
			PatternLabel: patternLabels[e.Exercise.Pattern],
			Equipment:    naoNilTexto(e.Exercise.Equipment),
			Muscles:      naoNilTexto(e.Exercise.Muscles),
			Impact:       string(e.Exercise.Impact),
			Cue:          e.Exercise.Cue,
			DemoQuery:    e.Exercise.DemoQuery,
			DemoURL:      e.Exercise.DemoURL,
		})
	}
	return SessionPlan{
		Focus: string(s.Focus), FocusLabel: focusLabels[s.Focus],
		Muscles: naoNilTexto(s.Muscles), BudgetMinutes: budgetMinutes,
		IsRecovery: c.IsRecoveryDay(planLabel), MainSets: principais,
		MethodPhrase: training.FraseDoMetodo(specialistID),
		Exercises:    exercicios,
	}
}

func naoNilTexto(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// Rótulos por papel. Aquecer não é uma série: chamar-lhe "SÉRIE 1 DE 1" logo a
// seguir ao "Prepara-te" fazia a preparação parecer trabalho.
var roleLabel = map[training.Role]string{
	training.Warmup:   "AQUECIMENTO",
	training.Main:     "SÉRIE",
	training.Cooldown: "ALONGAMENTO",
}

var blockName = map[training.Role]string{
	training.Warmup:   "Aquecimento",
	training.Main:     "Exercício",
	training.Cooldown: "Alongamento",
}

// hydrateTone — a hidratação tem cor própria porque não é treino.
const hydrateTone = "#6aa9ff"

// clockPlaceholder é o único campo que o cliente substitui.
//
// O relógio da série corre no dispositivo — contar segundos é apresentação. O
// que o servidor não pode deixar ao cliente é **decidir o que esses segundos
// significam**.
const clockPlaceholder = "{clock}"

// BuildSessionPackage transforma a sessão e a sua linha do tempo no pacote que
// o Modo Foco percorre.
func BuildSessionPackage(c training.Config, s training.Session, steps []training.Step) SessionPackage {
	// Sem o plano: para quem já chamava esta função e não sabe do rótulo nem do
	// treinador. `BuildSessionPackageCom` é a que os handlers usam.
	return BuildSessionPackageCom(c, s, steps, "", "", 0)
}

func BuildSessionPackageCom(
	c training.Config, s training.Session, steps []training.Step,
	planLabel, specialistID string, budgetMinutes int,
) SessionPackage {
	total := training.TotalSets(steps)
	sessionSeconds := training.RemainingSeconds(c, steps, 0)

	pkg := SessionPackage{
		SessionID:        s.ID,
		Title:            s.Title,
		Summary:          s.Summary,
		EstimatedSeconds: s.EstimatedSeconds,
		EstimatedKcal:    s.EstimatedKcal,
		TotalSets:        total,
		Steps:            make([]Step, 0, len(steps)),
		// Metade do tempo que a sessão pede. Generoso de propósito: quem treina
		// depressa continua a contar; quem só folheou o ecrã não.
		CompletionThresholdSeconds: sessionSeconds / 2,
	}

	for i, step := range steps {
		pkg.Steps = append(pkg.Steps, buildStep(c, s, steps, i, step, total))
	}
	pkg.Plan = buildSessionPlan(c, s, planLabel, specialistID, budgetMinutes)
	return pkg
}

func buildStep(c training.Config, s training.Session, steps []training.Step, index int, step training.Step, totalSets int) Step {
	out := Step{
		Index:    index,
		Kind:     string(step.Kind),
		Action:   actionFor(step),
		Progress: progressFor(c, s, steps, index, totalSets),
		Controls: controlsFor(s, steps, index, step),
		Tone:     toneFor(step),
	}

	if step.Kind == training.StepDone {
		out.Dial = Dial{Label: "FEITO", Value: ""}
		return out
	}

	// Passos a tempo avançam sozinhos. Uma série a repetições espera pela
	// pessoa: um relógio a correr contra quem está a fazer flexões muda o que
	// se faz, e não para melhor.
	if seconds, timed := autoAdvance(step); timed {
		out.AutoAdvanceAfterSeconds = &seconds
	}

	switch step.Kind {
	case training.StepGetReady:
		out.Dial = Dial{Label: "PREPARA-TE", Value: clockPlaceholder, Unit: "segundos"}
		out.Pill = fmt.Sprintf("%d exercícios · %d min",
			len(s.Exercises), roundMinutes(s.EstimatedSeconds))
		if e, ok := exerciseAt(s, step.ExerciseIndex); ok {
			out.Capsule = e.Exercise.Name
			out.Cue = e.Exercise.Cue
			out.Muscles = e.Exercise.Muscles
		}

	case training.StepHydrate:
		out.Dial = Dial{Label: "HIDRATAR", Value: clockPlaceholder, Unit: "segundos"}
		out.Pill = "Bebe alguns goles"
		if e, ok := exerciseAt(s, step.NextExerciseIndex); ok {
			out.Capsule = e.Exercise.Name
			out.Cue = e.Exercise.Cue
		}

	case training.StepRest:
		label := "MUDA DE EXERCÍCIO"
		if step.Reason == training.BetweenSets {
			label = "DESCANSO"
		}
		out.Dial = Dial{Label: label, Value: clockPlaceholder, Unit: "segundos"}
		if e, ok := exerciseAt(s, step.NextExerciseIndex); ok {
			out.Capsule = e.Exercise.Name
			// No descanso, a deixa é a do exercício que **vem**: chegar à série
			// seguinte já a saber o que fazer é o que o descanso serve para dar.
			out.Cue = e.Exercise.Cue
			if e.Role != training.Main {
				out.Pill = "A seguir · " + strings.ToLower(roleLabel[e.Role])
			} else {
				out.Pill = fmt.Sprintf("A seguir · série %d de %d", step.NextSetNumber, e.Sets)
			}
		}

	case training.StepSet:
		out.Dial = dialForSet(step)
		out.Pill = pillForSet(s, step)
		if e, ok := exerciseAt(s, step.ExerciseIndex); ok {
			out.Capsule = e.Exercise.Name
			out.Cue = e.Exercise.Cue
			out.Muscles = e.Exercise.Muscles
		}
	}

	return out
}

func dialForSet(step training.Step) Dial {
	// Uma série diz qual é de quantas; aquecer e alongar não se contam, e quando
	// há mais do que um passo dizem-no sem se fazerem passar por séries.
	label := roleLabel[step.Role]
	switch {
	case step.Role == training.Main:
		label = fmt.Sprintf("SÉRIE %d DE %d", step.SetNumber, step.TotalSets)
	case step.TotalSets > 1:
		label = fmt.Sprintf("%s %d/%d", roleLabel[step.Role], step.SetNumber, step.TotalSets)
	}

	if step.Measure == training.Time {
		return Dial{Label: label, Value: clockPlaceholder, Unit: "segundos"}
	}
	// Numa série a repetições o número grande é o **alvo**, não um relógio.
	return Dial{Label: label, Value: fmt.Sprint(step.Target), Unit: "repetições"}
}

// pillForSet — a posição aparece sempre, e sempre aqui.
//
// Antes dependia da medida do exercício, que nada tem a ver com posição: num
// aquecimento de dois, os círculos de braços (a tempo) diziam "Aquecimento 1 de
// 2" e a rotação torácica (a repetições) diziam só o cronómetro.
//
// E conta **dentro do bloco**: quem estava no segundo aquecimento lia
// "Exercício 2 de 9", concluía que já estava no treino, e depois não percebia
// porque é que o botão ainda dizia "Já aqueci".
func pillForSet(s training.Session, step training.Step) string {
	block := training.BlockPositionOf(s, step.ExerciseIndex)
	if block == nil {
		if step.Measure == training.Time {
			return ""
		}
		return clockPlaceholder + " nesta série"
	}
	where := fmt.Sprintf("%s %d de %d", blockName[block.Role], block.Position, block.Total)
	// O cronómetro só se junta quando o mostrador não é já um relógio: numa
	// série a tempo o número grande é a contagem, e repeti-la seria ruído.
	if step.Measure == training.Time {
		return where
	}
	return where + " · " + clockPlaceholder
}

func actionFor(step training.Step) Action {
	switch step.Kind {
	case training.StepGetReady:
		return Action{Label: "Começar", Icon: "play-skip-forward"}
	case training.StepRest:
		return Action{Label: "Saltar", Icon: "play-skip-forward"}
	case training.StepHydrate:
		return Action{Label: "Já bebi", Icon: "play-skip-forward"}
	case training.StepDone:
		return Action{Label: "Concluir", Icon: "checkmark"}
	case training.StepSet:
		// O visto é para séries de trabalho e mais nada. Num círculo cheio e
		// lima, um visto lê-se como "concluído" — e a aparecer no instante em
		// que a contagem do "Prepara-te" chega a zero, dava a entender que a
		// preparação tinha sido dada por feita.
		switch step.Role {
		case training.Warmup:
			return Action{Label: "Já aqueci", Icon: "arrow-forward"}
		case training.Cooldown:
			return Action{Label: "Já alonguei", Icon: "arrow-forward"}
		default:
			return Action{Label: "Série feita", Icon: "checkmark"}
		}
	}
	return Action{Label: "Seguinte", Icon: "play-skip-forward"}
}

func toneFor(step training.Step) string {
	if step.Kind == training.StepHydrate {
		return hydrateTone
	}
	return ""
}

func autoAdvance(step training.Step) (int, bool) {
	switch step.Kind {
	case training.StepGetReady, training.StepRest, training.StepHydrate:
		return step.Seconds, true
	case training.StepSet:
		if step.Measure == training.Time {
			return step.Target, true
		}
	}
	return 0, false
}

func progressFor(c training.Config, s training.Session, steps []training.Step, index, totalSets int) Progress {
	p := Progress{Sets: SetProgress{Done: training.SetsDone(steps, index), Total: totalSets}}

	slices := training.BlockSlices(c, s, steps, index, 0)
	total := 0
	for _, sl := range slices {
		total += sl.Seconds
	}
	for _, sl := range slices {
		block := BlockProgress{Role: string(sl.Role)}
		if total > 0 {
			block.Weight = round4(float64(sl.Seconds) / float64(total))
		}
		if sl.Seconds > 0 {
			block.Filled = round4(float64(sl.Done) / float64(sl.Seconds))
		}
		p.Blocks = append(p.Blocks, block)
	}
	return p
}

// controlsFor resolve as guardas dos controlos.
//
// INVARIANTE 12: só se remove uma série **à frente** da posição actual.
// Remover uma que já passou encurtaria o passado — e o histórico deixaria de
// dizer o que aconteceu.
func controlsFor(s training.Session, steps []training.Step, index int, step training.Step) Controls {
	at, ok := targetExercise(step)
	if !ok || index >= len(steps) {
		return Controls{
			AddSet:    Control{Enabled: false, Reason: "no_exercise"},
			RemoveSet: Control{Enabled: false, Reason: "no_exercise"},
		}
	}

	entry, exists := exerciseAt(s, at)
	if !exists {
		return Controls{
			AddSet:    Control{Enabled: false, Reason: "no_exercise"},
			RemoveSet: Control{Enabled: false, Reason: "no_exercise"},
		}
	}

	controls := Controls{AddSet: Control{Enabled: true}}

	// Quantas séries deste exercício ainda estão à frente.
	ahead := 0
	for i := index + 1; i < len(steps); i++ {
		if steps[i].Kind == training.StepSet && steps[i].ExerciseIndex == at {
			ahead++
		}
	}

	switch {
	case entry.Sets <= 1:
		// Um exercício sem séries não é um exercício — invariante 13.
		controls.RemoveSet = Control{Enabled: false, Reason: "last_set_of_exercise"}
	case ahead == 0:
		controls.RemoveSet = Control{Enabled: false, Reason: "no_set_ahead"}
	default:
		controls.RemoveSet = Control{Enabled: true}
	}
	return controls
}

// targetExercise — a que exercício se refere este passo.
//
// Em descanso ou hidratação vale o que **vem a seguir**, que é aquele em que a
// pessoa está a pensar quando carrega no "+ Série".
func targetExercise(step training.Step) (int, bool) {
	switch step.Kind {
	case training.StepSet, training.StepGetReady:
		return step.ExerciseIndex, true
	case training.StepRest, training.StepHydrate:
		return step.NextExerciseIndex, true
	}
	return 0, false
}

func exerciseAt(s training.Session, i int) (training.SessionExercise, bool) {
	if i < 0 || i >= len(s.Exercises) {
		return training.SessionExercise{}, false
	}
	return s.Exercises[i], true
}

func roundMinutes(seconds int) int { return (seconds + 30) / 60 }

func round4(v float64) float64 { return float64(int(v*10000+0.5)) / 10000 }
