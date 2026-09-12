package training

import (
	"fmt"
	"testing"
)

// Os invariantes 8 a 13 de docs/backend/03-regras-de-negocio.md §5.
//
// Não são números: são propriedades. Cada um corresponde a um defeito que já
// aconteceu — repartições que não somam, progresso que recua, blocos que enchem
// antes de tempo.

// sessions gera um leque largo de sessões reais para as propriedades correrem
// sobre elas.
func sessions(t *testing.T) []Session {
	t.Helper()
	c := DefaultConfig()
	var out []Session
	for _, label := range []string{"Upper Body", "Lower Body", "Cardio", "Full Body", "Mobility", "Recovery"} {
		for _, exp := range []Experience{Beginner, Intermediate, Advanced} {
			for _, min := range []int{20, 38, 60} {
				for _, eq := range [][]string{nil, {"dumbbells"}, {"barbell", "machines", "cardio"}} {
					s, err := BuildSession(c, BuildInput{
						PlanLabel: label, Experience: exp, Equipment: eq,
						Minutes: min, DayISO: "2026-09-12",
					})
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// INVARIANTE 8 — as fatias dos blocos somam a sessão inteira.
func TestInvariant08_BlockSlicesSumToTheWholeSession(t *testing.T) {
	c := DefaultConfig()
	for _, s := range sessions(t) {
		steps := BuildTimeline(c, s)
		total := RemainingSeconds(c, steps, 0)
		sum := 0
		for _, slice := range BlockSlices(c, s, steps, 0, 0) {
			sum += slice.Seconds
		}
		if sum != total {
			t.Fatalf("%s: os blocos somam %ds, a sessão tem %ds", s.ID, sum, total)
		}
	}
}

// INVARIANTE 9 — o progresso nunca recua entre passos consecutivos.
// Uma barra que anda para trás é um erro.
func TestInvariant09_ProgressNeverGoesBackwards(t *testing.T) {
	c := DefaultConfig()
	for _, s := range sessions(t) {
		steps := BuildTimeline(c, s)
		prevSets, prevDone := 0, 0
		for i := range steps {
			if n := SetsDone(steps, i); n < prevSets {
				t.Fatalf("%s: séries feitas passaram de %d para %d no passo %d", s.ID, prevSets, n, i)
			} else {
				prevSets = n
			}
			done := 0
			for _, slice := range BlockSlices(c, s, steps, i, 0) {
				done += slice.Done
			}
			if done < prevDone {
				t.Fatalf("%s: progresso passou de %ds para %ds no passo %d", s.ID, prevDone, done, i)
			}
			prevDone = done
		}
	}
}

// INVARIANTE 10 — o bloco actual nunca está 100% cheio antes de ser deixado.
// Apanhou um erro real de repartição.
func TestInvariant10_CurrentBlockIsNeverFullBeforeLeavingIt(t *testing.T) {
	c := DefaultConfig()
	for _, s := range sessions(t) {
		steps := BuildTimeline(c, s)
		for i, step := range steps {
			if step.Kind == StepDone {
				continue
			}
			role, ok := stepRole(s, step)
			if !ok {
				continue
			}
			// No início do passo: ainda não se gastou nada dele.
			for _, slice := range BlockSlices(c, s, steps, i, 0) {
				if slice.Role != role {
					continue
				}
				if slice.Seconds > 0 && slice.Done >= slice.Seconds {
					t.Fatalf("%s passo %d (%s): bloco %s já cheio (%d/%d) e ainda se está nele",
						s.ID, i, step.Kind, role, slice.Done, slice.Seconds)
				}
			}
		}
	}
}

// INVARIANTE 11 — blocos anteriores completos, seguintes por começar.
// Os descansos contam para onde **vão**, não de onde vêm.
func TestInvariant11_PastBlocksFullFutureBlocksEmpty(t *testing.T) {
	c := DefaultConfig()
	order := map[Role]int{Warmup: 0, Main: 1, Cooldown: 2}
	for _, s := range sessions(t) {
		steps := BuildTimeline(c, s)
		for i, step := range steps {
			if step.Kind == StepDone {
				continue
			}
			role, ok := stepRole(s, step)
			if !ok {
				continue
			}
			for _, slice := range BlockSlices(c, s, steps, i, 0) {
				switch {
				case order[slice.Role] < order[role]:
					if slice.Done != slice.Seconds {
						t.Fatalf("%s passo %d: bloco %s já passou e está a %d/%d",
							s.ID, i, slice.Role, slice.Done, slice.Seconds)
					}
				case order[slice.Role] > order[role]:
					if slice.Done != 0 {
						t.Fatalf("%s passo %d: bloco %s ainda não começou e está a %d",
							s.ID, i, slice.Role, slice.Done)
					}
				}
			}
		}
	}
}

// INVARIANTE 13 — `sets ≥ 1` sempre. Um exercício sem séries não é um exercício.
func TestInvariant13_EveryExerciseHasAtLeastOneSet(t *testing.T) {
	for _, s := range sessions(t) {
		for _, e := range s.Exercises {
			if e.Sets < 1 {
				t.Fatalf("%s: %s com %d séries", s.ID, e.Exercise.ID, e.Sets)
			}
			if e.Target < 1 {
				t.Fatalf("%s: %s com alvo %d", s.ID, e.Exercise.ID, e.Target)
			}
		}
	}
}

// Aquecer e alongar não contam como séries — a regra que mais confusão causou.
func TestSetsCountOnlyRealWork(t *testing.T) {
	c := DefaultConfig()
	for _, s := range sessions(t) {
		steps := BuildTimeline(c, s)
		work := 0
		for _, e := range s.Exercises {
			if e.Role == Main {
				work += e.Sets
			}
		}
		if got := TotalSets(steps); got != work {
			t.Fatalf("%s: %d séries contadas, %d de trabalho", s.ID, got, work)
		}
		// E durante uma série, ela ainda não conta.
		for i, step := range steps {
			if step.Kind == StepSet && step.Role == Main {
				before := SetsDone(steps, i)
				after := SetsDone(steps, i+1)
				if after != before+1 {
					t.Fatalf("%s passo %d: a série só conta ao ser deixada (%d → %d)", s.ID, i, before, after)
				}
				break
			}
		}
	}
}

// Uma sessão tem sempre aquecimento e alongamento — não são opcionais.
func TestEverySessionHasWarmupAndCooldown(t *testing.T) {
	for _, s := range sessions(t) {
		var w, m, cd int
		for _, e := range s.Exercises {
			switch e.Role {
			case Warmup:
				w++
			case Main:
				m++
			case Cooldown:
				cd++
			}
		}
		if w == 0 || cd == 0 || m == 0 {
			t.Fatalf("%s: %d aquecimento, %d trabalho, %d alongamento", s.ID, w, m, cd)
		}
	}
}

// A ordem é aquecer, trabalhar, alongar. Nunca outra.
func TestBlocksAreInOrder(t *testing.T) {
	order := map[Role]int{Warmup: 0, Main: 1, Cooldown: 2}
	for _, s := range sessions(t) {
		last := -1
		for _, e := range s.Exercises {
			if order[e.Role] < last {
				t.Fatalf("%s: %s (%s) depois de um bloco posterior", s.ID, e.Exercise.ID, e.Role)
			}
			last = order[e.Role]
		}
	}
}

// Nenhum exercício aparece duas vezes na mesma sessão.
func TestNoDuplicateExercises(t *testing.T) {
	for _, s := range sessions(t) {
		seen := map[string]bool{}
		for _, e := range s.Exercises {
			if seen[e.Exercise.ID] {
				t.Fatalf("%s: %s aparece duas vezes", s.ID, e.Exercise.ID)
			}
			seen[e.Exercise.ID] = true
		}
	}
}

// Um exercício excluído nunca aparece, em nenhum bloco.
func TestExcludedNeverAppears(t *testing.T) {
	c := DefaultConfig()
	excluded := []string{"pushup", "burpee", "plank", "cat_cow", "child_pose"}
	for _, label := range []string{"Upper Body", "Full Body", "Mobility", "Recovery"} {
		s, err := BuildSession(c, BuildInput{
			PlanLabel: label, Experience: Intermediate, Minutes: 45,
			DayISO: "2026-09-12", Excluded: excluded,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range s.Exercises {
			for _, id := range excluded {
				if e.Exercise.ID == id {
					t.Fatalf("%s: %q foi excluído e apareceu como %s", label, id, e.Role)
				}
			}
		}
	}
}

// Fixar um exercício não remonta a sessão toda: a semente fica de fora das
// preferências de propósito.
func TestPinningDoesNotReshuffleTheSession(t *testing.T) {
	c := DefaultConfig()
	// Com halteres no perfil: um fixado que a pessoa não consegue fazer é
	// omitido de propósito, e o teste não testaria nada.
	in := BuildInput{PlanLabel: "Full Body", Experience: Intermediate, Minutes: 45,
		DayISO: "2026-09-12", Equipment: []string{"dumbbells"}}
	plain, err := BuildSession(c, in)
	if err != nil {
		t.Fatal(err)
	}

	in.Pinned = []string{"db_row"}
	pinned, err := BuildSession(c, in)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, e := range pinned.Exercises {
		if e.Exercise.ID == "db_row" {
			found = true
			if e.Role != Main {
				t.Fatalf("um fixado entra no bloco principal, entrou como %s", e.Role)
			}
		}
	}
	if !found {
		t.Fatalf("o fixado não entrou na sessão: %v", rolesOf(pinned, Main))
	}

	// O aquecimento e o alongamento não podem mudar por se ter fixado trabalho.
	before := rolesOf(plain, Warmup)
	after := rolesOf(pinned, Warmup)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("fixar trabalho mudou o aquecimento: %v → %v", before, after)
	}
}

// Uma prescrição fixada sobrevive ao acerto ao orçamento. Quem escreve 4×10
// recebe 4×10 — ou uma duração maior, dita como está.
func TestFixedPrescriptionSurvivesBudgeting(t *testing.T) {
	c := DefaultConfig()
	for _, min := range []int{20, 30, 45, 60} {
		s, err := BuildSession(c, BuildInput{
			PlanLabel: "Full Body", Experience: Intermediate, Minutes: min, DayISO: "2026-09-12",
			Pinned:        []string{"bodyweight_squat"},
			Prescriptions: map[string]Prescription{"bodyweight_squat": {Sets: 5, Target: 15}},
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range s.Exercises {
			if e.Exercise.ID == "bodyweight_squat" && (e.Sets != 5 || e.Target != 15) {
				t.Fatalf("%d min: fixou 5×15 e recebeu %dx%d", min, e.Sets, e.Target)
			}
		}
	}
}

func rolesOf(s Session, role Role) []string {
	var out []string
	for _, e := range s.Exercises {
		if e.Role == role {
			out = append(out, e.Exercise.ID)
		}
	}
	return out
}

func mustGet(t *testing.T, id string) Exercise {
	t.Helper()
	e, ok := GetExercise(id)
	if !ok {
		t.Fatalf("exercício %q não existe", id)
	}
	return e
}
