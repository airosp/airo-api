package training

// HydrationCount — quantas pausas de hidratação cabem numa sessão com este
// número de exercícios principais.
func HydrationCount(c Config, mainExerciseCount int) int {
	if mainExerciseCount < c.Hydration.MinExercisesForBreak {
		return 0
	}
	return (mainExerciseCount - 1) / c.Hydration.EveryExercises
}

// BuildTimeline transforma a sessão numa lista de passos.
//
// Calcula-se de uma vez, e não passo a passo à medida que a pessoa avança:
// assim o progresso, o tempo que falta e o "a seguir" saem todos da mesma
// lista. Montada à medida, eram três contas independentes que mais cedo ou mais
// tarde discordavam entre si.
func BuildTimeline(c Config, s Session) []Step {
	steps := []Step{{Kind: StepGetReady, Seconds: c.GetReadySeconds, ExerciseIndex: 0}}
	last := len(s.Exercises) - 1
	breaks := hydrationBreakpoints(c, s)

	for exerciseIndex, entry := range s.Exercises {
		for setNumber := 1; setNumber <= entry.Sets; setNumber++ {
			steps = append(steps, Step{
				Kind:          StepSet,
				ExerciseIndex: exerciseIndex,
				Role:          entry.Role,
				SetNumber:     setNumber,
				TotalSets:     entry.Sets,
				Measure:       entry.Exercise.Measure,
				Target:        entry.Target,
			})

			isLastSet := setNumber == entry.Sets
			if isLastSet && exerciseIndex == last {
				break
			}

			if isLastSet {
				if breaks[exerciseIndex] {
					steps = append(steps, Step{
						Kind:              StepHydrate,
						Seconds:           c.Hydration.Seconds,
						NextExerciseIndex: exerciseIndex + 1,
					})
				}
				steps = append(steps, Step{
					Kind:              StepRest,
					Seconds:           entry.RestSeconds + c.TransitionBonusSeconds,
					Reason:            BetweenExercises,
					NextExerciseIndex: exerciseIndex + 1,
					NextSetNumber:     1,
				})
			} else {
				steps = append(steps, Step{
					Kind:              StepRest,
					Seconds:           entry.RestSeconds,
					Reason:            BetweenSets,
					NextExerciseIndex: exerciseIndex,
					NextSetNumber:     setNumber + 1,
				})
			}
		}
	}

	return append(steps, Step{Kind: StepDone})
}

// hydrationBreakpoints — depois de que exercícios entra uma pausa para beber.
func hydrationBreakpoints(c Config, s Session) map[int]bool {
	// Aquecimento e arrefecimento ficam de fora: uma pausa para beber logo a
	// seguir aos círculos de braços não faz sentido nenhum.
	var mainIndexes []int
	for i, e := range s.Exercises {
		if e.Role == Main {
			mainIndexes = append(mainIndexes, i)
		}
	}

	breaks := map[int]bool{}
	count := HydrationCount(c, len(mainIndexes))
	if count == 0 {
		return breaks
	}

	every := c.Hydration.EveryExercises
	for n := 1; n <= count; n++ {
		idx := n*every - 1
		if idx < 0 || idx >= len(mainIndexes) {
			continue
		}
		at := mainIndexes[idx]
		// Nunca na última posição: interromperia para beber e logo a seguir
		// acabava.
		if at < len(s.Exercises)-1 {
			breaks[at] = true
		}
	}
	return breaks
}

// StepSeconds — quanto dura um passo. Uma série a repetições não tem duração
// fixa: estima-se.
func StepSeconds(c Config, step Step) int {
	switch step.Kind {
	case StepGetReady, StepHydrate, StepRest:
		return step.Seconds
	case StepSet:
		if step.Measure == Time {
			return step.Target
		}
		return step.Target * c.SecondsPerRep
	default:
		return 0
	}
}

// RemainingSeconds — segundos que faltam a partir deste passo.
func RemainingSeconds(c Config, steps []Step, fromIndex int) int {
	total := 0
	for i := fromIndex; i < len(steps); i++ {
		total += StepSeconds(c, steps[i])
	}
	return total
}

// TotalSets — quantas séries de **trabalho** a sessão tem.
//
// Aquecer e alongar não entram. Contavam, e por isso o mostrador anunciava
// dezanove séries num dia em que doze eram trabalho e as outras sete eram
// círculos de braços e a postura da criança.
func TotalSets(steps []Step) int {
	n := 0
	for _, s := range steps {
		if s.Kind == StepSet && s.Role == Main {
			n++
		}
	}
	return n
}

// SetsDone — séries de trabalho que já ficaram para trás.
//
// Conta as **terminadas**, não a que está a decorrer: durante a SÉRIE 1 DE 3 o
// contador diz 0, porque não se conta como feita uma série a meio.
func SetsDone(steps []Step, index int) int {
	if index > len(steps) {
		index = len(steps)
	}
	if index < 0 {
		index = 0
	}
	return TotalSets(steps[:index])
}

type BlockPosition struct {
	Role     Role `json:"role"`
	Position int  `json:"position"`
	Total    int  `json:"total"`
}

// BlockPositionOf — onde está este exercício dentro do **seu próprio bloco**.
//
// O ecrã contava "Exercício 2 de 9" sobre os exercícios todos. Quem estava no
// segundo aquecimento lia "2 de 9", concluía que já estava no treino, e depois
// não percebia porque é que o botão ainda dizia "Já aqueci".
func BlockPositionOf(s Session, exerciseIndex int) *BlockPosition {
	if exerciseIndex < 0 || exerciseIndex >= len(s.Exercises) {
		return nil
	}
	role := s.Exercises[exerciseIndex].Role
	total, position := 0, 0
	for i, e := range s.Exercises {
		if e.Role != role {
			continue
		}
		total++
		if i <= exerciseIndex {
			position++
		}
	}
	return &BlockPosition{Role: role, Position: position, Total: total}
}

type BlockSlice struct {
	Role Role `json:"role"`
	// Seconds que o bloco ocupa na sessão.
	Seconds int `json:"seconds"`
	// Done — segundos já passados dentro do bloco.
	Done int `json:"done"`
}

// stepRole — a que bloco pertence um passo.
//
// Os descansos contam para onde **vão**, não de onde vêm: o descanso depois do
// último aquecimento já é tempo do bloco principal, porque é o que prepara a
// primeira série.
func stepRole(s Session, step Step) (Role, bool) {
	var at int
	switch step.Kind {
	case StepSet, StepGetReady:
		at = step.ExerciseIndex
	case StepRest, StepHydrate:
		at = step.NextExerciseIndex
	default:
		return "", false
	}
	if at < 0 || at >= len(s.Exercises) {
		return "", false
	}
	return s.Exercises[at].Role, true
}

// BlockSlices — a sessão repartida pelos seus blocos, com o que já passou em
// cada um.
//
// Existe porque uma barra única mentia por omissão: dizer "Aquecimento 2 de 2"
// ao lado de uma barra a 8% põe duas escalas diferentes lado a lado — uma
// relativa ao bloco, outra à sessão — e o que se lê é que a barra está parada.
func BlockSlices(c Config, s Session, steps []Step, index, elapsedInStep int) []BlockSlice {
	totals := map[Role]*BlockSlice{}

	for position, step := range steps {
		if step.Kind == StepDone {
			continue
		}
		role, ok := stepRole(s, step)
		if !ok {
			role = Main
		}
		seconds := StepSeconds(c, step)
		slice, exists := totals[role]
		if !exists {
			slice = &BlockSlice{Role: role}
			totals[role] = slice
		}
		slice.Seconds += seconds
		switch {
		case position < index:
			slice.Done += seconds
		case position == index:
			e := elapsedInStep
			if e < 0 {
				e = 0
			}
			if e > seconds {
				e = seconds
			}
			slice.Done += e
		}
	}

	// Ordem fixa. Em Go a iteração de um mapa é aleatória, e sem isto os blocos
	// apareciam por ordens diferentes a cada pedido.
	out := make([]BlockSlice, 0, 3)
	for _, role := range []Role{Warmup, Main, Cooldown} {
		if slice, ok := totals[role]; ok && slice.Seconds > 0 {
			out = append(out, *slice)
		}
	}
	return out
}
