package training

import (
	"fmt"

	"github.com/airosp/airo-api/internal/engine/portable"
)

type BuildInput struct {
	// PlanLabel é o rótulo do plano semanal: "Upper Body", "Recovery", …
	PlanLabel  string
	Experience Experience
	Equipment  []string
	// Minutes é o orçamento, não uma sugestão.
	Minutes int
	// DayISO entra na semente para o treino ser estável.
	DayISO string

	// Pinned — exercícios que a pessoa fixou, pela ordem em que os fixou.
	// Entram sempre, antes do resto, e o acerto ao orçamento nunca os corta.
	Pinned []string
	// Excluded — exercícios a nunca propor.
	Excluded []string
	// Prescriptions — séries e alvo fixados pela pessoa. Quando existem, mandam
	// sobre o cálculo, e o acerto ao orçamento deixa de lhes mexer: senão a
	// pessoa escrevia 4×10 e recebia 3×10 sem ninguém lhe dizer porquê.
	Prescriptions map[string]Prescription
}

func setSeconds(c Config, e SessionExercise) int {
	if e.Exercise.Measure == Time {
		return e.Target
	}
	return e.Target * c.SecondsPerRep
}

// ExerciseSeconds — quanto custa um exercício inteiro, séries e descansos.
func ExerciseSeconds(c Config, e SessionExercise) int {
	// O descanso da última série é o da transição, contado uma vez só.
	return e.Sets*setSeconds(c, e) + (e.Sets-1)*e.RestSeconds
}

func buildEntry(c Config, e Exercise, exp Experience, fixed *Prescription) SessionExercise {
	sets := c.SetsByExperience[exp]
	target := c.RepsByPattern[e.Pattern]
	if e.Measure == Time {
		target = c.TimeByPattern[e.Pattern]
	}
	if fixed != nil {
		sets, target = fixed.Sets, fixed.Target
	}
	rest := int(portable.RoundJS(float64(c.RestByPattern[e.Pattern]) * c.RestFactorByExperience[exp]))
	return SessionExercise{Exercise: e, Role: Main, Sets: sets, Target: target, RestSeconds: rest}
}

// BuildSession monta a sessão do dia.
//
// O tempo disponível é um orçamento que se gasta: enche-se com aquecimento e
// arrefecimento primeiro — que não são opcionais — e o que sobra decide quantos
// exercícios entram. Um plano de 20 minutos não é um de 60 encurtado à pressa.
func BuildSession(c Config, in BuildInput) (Session, error) {
	lib, err := Library()
	if err != nil {
		return Session{}, err
	}

	focus, ok := c.FocusByPlanLabel[in.PlanLabel]
	if !ok {
		focus = FocusFull
	}

	// As preferências ficam **fora** da semente de propósito: entrando nela,
	// fixar um exercício remontava a sessão toda de um dia para o outro. Quem
	// fixa um agachamento quer o agachamento, não um treino diferente.
	seed := portable.SeedFrom(fmt.Sprintf("%s|%s|%s|%s",
		in.DayISO, in.PlanLabel, in.Experience, joinComma(in.Equipment)))
	rank := levelRank[in.Experience]

	blocked := toSet(in.Excluded)
	var pool []Exercise
	for _, e := range lib {
		if !blocked[e.ID] && isAvailable(e, in.Equipment) && levelRank[e.Level] <= rank {
			pool = append(pool, e)
		}
	}

	poolByID := make(map[string]Exercise, len(pool))
	for _, e := range pool {
		poolByID[e.ID] = e
	}

	// Os fixados saem da `pool` e não da biblioteca: um exercício fixado que
	// exige halteres deixa de ser possível quando os halteres saem do perfil, e
	// nesse caso é omitido em vez de aparecer numa sessão impossível.
	//
	// Num dia de recuperação ficam todos de fora. "Sempre no plano" quer dizer
	// sempre que se treina, e um dia de recuperação com flexões deixa de ser um
	// dia de recuperação.
	var pinnedExercises []Exercise
	if !c.IsRecoveryDay(in.PlanLabel) {
		seen := map[string]bool{}
		for _, id := range in.Pinned {
			if seen[id] {
				continue
			}
			seen[id] = true
			if e, ok := poolByID[id]; ok {
				pinnedExercises = append(pinnedExercises, e)
			}
		}
	}
	pinnedIDs := map[string]bool{}
	for _, e := range pinnedExercises {
		pinnedIDs[e.ID] = true
	}

	// Fora do aquecimento e do alongamento: um exercício fixado que também
	// serve para aquecer aparecia duas vezes na mesma sessão.
	warmup := portable.Pick(filter(pool, func(e Exercise) bool {
		return e.Warmup && !pinnedIDs[e.ID]
	}), 2, seed)

	// Há exercícios que servem para aquecer e para arrefecer (gato-camelo,
	// rotação torácica). Sem excluir os já escolhidos, o mesmo aparecia duas
	// vezes na mesma sessão — e com a mesma chave na lista do resumo.
	warmupIDs := map[string]bool{}
	for _, e := range warmup {
		warmupIDs[e.ID] = true
	}
	cooldown := portable.Pick(filter(pool, func(e Exercise) bool {
		return e.Cooldown && !warmupIDs[e.ID] && !pinnedIDs[e.ID]
	}), 2, seed+17)

	// O alvo tem de seguir a medida do exercício: `timeByPattern` num exercício
	// a repetições dava "Alongamento do escalador 1×45" — quarenta e cinco
	// repetições de um alongamento, a aquecer.
	prep := func(e Exercise, role Role, rest int) SessionExercise {
		sets, target := 1, c.RepsByPattern[e.Pattern]
		if e.Measure == Time {
			target = c.TimeByPattern[Mobility]
		}
		if p, ok := in.Prescriptions[e.ID]; ok {
			// Quem alongou 60 s uma vez e disse "fica assim" não quer voltar a
			// encontrar 45 s no dia seguinte.
			sets, target = p.Sets, p.Target
		}
		return SessionExercise{Exercise: e, Role: role, Sets: sets, Target: target, RestSeconds: rest}
	}

	warmupEntries := make([]SessionExercise, 0, len(warmup))
	for _, e := range warmup {
		warmupEntries = append(warmupEntries, prep(e, Warmup, 12))
	}
	cooldownEntries := make([]SessionExercise, 0, len(cooldown))
	for _, e := range cooldown {
		cooldownEntries = append(cooldownEntries, prep(e, Cooldown, 10))
	}

	fixedSeconds := c.GetReadySeconds
	for _, e := range append(append([]SessionExercise{}, warmupEntries...), cooldownEntries...) {
		fixedSeconds += ExerciseSeconds(c, e)
	}
	budget := in.Minutes*60 - fixedSeconds

	used := map[string]bool{}
	for _, e := range warmup {
		used[e.ID] = true
	}
	for _, e := range cooldown {
		used[e.ID] = true
	}
	for _, e := range pinnedExercises {
		used[e.ID] = true
	}

	// Os fixados abrem o bloco principal. Ficando no fim, o acerto ao orçamento
	// — que corta pelo fim — comia-os primeiro.
	main := make([]SessionExercise, 0, c.ExerciseCountRange.Max)
	for _, e := range pinnedExercises {
		main = append(main, buildEntry(c, e, in.Experience, prescriptionOf(in.Prescriptions, e.ID)))
	}
	pinnedCount := len(main)
	for _, e := range main {
		budget -= ExerciseSeconds(c, e) + c.TransitionBonusSeconds
	}

	// `chosen` conta o que o **motor** escolheu, e não `len(main)`: com os
	// fixados a abrir a lista, `len(main)` começava em 1 e deslocava o índice de
	// todas as escolhas seguintes — fixar uma flexão trocava sete exercícios que
	// não tinham nada a ver com ela.
	chosen := 0
	wanted := c.PatternsByFocus[focus]

	for round := 0; round < 3 && len(main) < c.ExerciseCountRange.Max; round++ {
		for _, pattern := range wanted {
			if len(main) >= c.ExerciseCountRange.Max {
				break
			}
			// Aquecer e arrefecer não é treinar: o bloco principal só vai buscar
			// movimentos que sejam trabalho.
			candidates := filter(pool, func(e Exercise) bool {
				return e.Pattern == pattern && !used[e.ID] && !e.Warmup && !e.Cooldown
			})
			if len(candidates) == 0 {
				continue
			}
			e := candidates[portable.Mod(seed+chosen*7+round, len(candidates))]
			entry := buildEntry(c, e, in.Experience, prescriptionOf(in.Prescriptions, e.ID))
			cost := ExerciseSeconds(c, entry) + c.TransitionBonusSeconds
			// Abaixo do mínimo aceita-se estourar o orçamento: uma sessão de
			// dois exercícios não é um treino, é uma desculpa.
			if cost > budget && len(main) >= c.ExerciseCountRange.Min {
				continue
			}
			used[e.ID] = true
			main = append(main, entry)
			chosen++
			budget -= cost
		}
		if budget <= 0 {
			break
		}
	}

	draftWith := func(entries []SessionExercise) Session {
		return Session{
			ID: fmt.Sprintf("w_%s_%s", in.DayISO, focus), Title: c.FocusTitles[focus],
			Focus: focus, Exercises: entries,
		}
	}
	assemble := func(mainEntries []SessionExercise) []SessionExercise {
		out := make([]SessionExercise, 0, len(warmupEntries)+len(mainEntries)+len(cooldownEntries))
		out = append(out, warmupEntries...)
		out = append(out, mainEntries...)
		return append(out, cooldownEntries...)
	}
	// O custo real de uma montagem percorre a linha do tempo que vai mesmo ser
	// executada. Estimar à parte foi o que produziu 59 minutos onde se pediram
	// 45 — havia dois modelos de custo a discordar, e o que contava era o outro.
	measure := func(mainEntries []SessionExercise) int {
		return RemainingSeconds(c, BuildTimeline(c, draftWith(assemble(mainEntries))), 0)
	}

	target := in.Minutes * 60
	// Nunca abaixo do mínimo, e nunca um fixado: cortá-lo era desfazer em
	// silêncio a escolha de quem pediu para ele lá estar.
	canDropExercise := func() bool {
		return len(main) > c.ExerciseCountRange.Min && len(main) > pinnedCount
	}
	// Um exercício com séries fixadas está fora do acerto: tirar-lhe uma série
	// para o treino caber nos minutos era desdizer, em silêncio, o número que a
	// pessoa acabou de escrever.
	tunableIdx := func(byFattest bool) int {
		best := -1
		for i := range main {
			if _, fixed := in.Prescriptions[main[i].Exercise.ID]; fixed {
				continue
			}
			if best == -1 {
				best = i
				continue
			}
			if byFattest && main[i].Sets > main[best].Sets {
				best = i
			}
			if !byFattest && main[i].Sets < main[best].Sets {
				best = i
			}
		}
		return best
	}
	base := c.SetsByExperience[in.Experience]
	floor := base - 1
	if floor < 2 {
		floor = 2
	}
	ceiling := base + c.ExtraSetsCap

	// Converge para o orçamento: cresce enquanto sobra tempo, encolhe enquanto
	// falta, e pára quando nenhum dos lados tem folga.
	for guard := 0; guard < 40; guard++ {
		seconds := float64(measure(main))
		switch {
		case seconds > float64(target)*1.06:
			fattest := tunableIdx(true)
			// Corta exercícios antes de cortar séries. Ao contrário, um treino
			// de 45 minutos saía com treze exercícios a duas séries — muita
			// coisa começada e nada trabalhado.
			if canDropExercise() && fattest != -1 && main[fattest].Sets <= base {
				main = main[:len(main)-1]
			} else if fattest != -1 && main[fattest].Sets > floor {
				main[fattest].Sets--
			} else if canDropExercise() {
				main = main[:len(main)-1]
			} else {
				guard = 40
			}
		case seconds < float64(target)*0.94:
			leanest := tunableIdx(false)
			if leanest != -1 && main[leanest].Sets < ceiling {
				main[leanest].Sets++
			} else {
				guard = 40
			}
		default:
			guard = 40
		}
	}

	all := assemble(main)
	muscles := uniqueMuscles(main)
	draft := draftWith(all)
	estimated := RemainingSeconds(c, BuildTimeline(c, draft), 0)

	summary := "Corpo inteiro"
	if len(muscles) > 0 {
		n := len(muscles)
		if n > 3 {
			n = 3
		}
		summary = joinList(muscles[:n], ", ")
	}

	draft.Muscles = muscles
	draft.Summary = summary
	draft.EstimatedSeconds = estimated
	draft.EstimatedKcal = int(portable.RoundJS(float64(estimated) / 60 * c.KcalPerMinute[focus]))
	return draft, nil
}

func prescriptionOf(m map[string]Prescription, id string) *Prescription {
	if p, ok := m[id]; ok {
		return &p
	}
	return nil
}

func filter(in []Exercise, keep func(Exercise) bool) []Exercise {
	out := make([]Exercise, 0, len(in))
	for _, e := range in {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// uniqueMuscles preserva a ordem de aparição: é um `Set` em TypeScript, que
// também a preserva. Um `map` em Go não preservaria, e o resumo da sessão
// mudava de ordem a cada pedido.
func uniqueMuscles(entries []SessionExercise) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range entries {
		for _, m := range e.Exercise.Muscles {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func joinComma(items []string) string { return joinList(items, ",") }

func joinList(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
