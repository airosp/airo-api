package view

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/airosp/airo-api/internal/engine/training"
)

func build(t *testing.T, label string, exp training.Experience, minutes int) (training.Config, training.Session, []training.Step, SessionPackage) {
	t.Helper()
	c := training.DefaultConfig()
	s, err := training.BuildSession(c, training.BuildInput{
		PlanLabel: label, Experience: exp, Equipment: []string{"dumbbells"},
		Minutes: minutes, DayISO: "2026-09-12",
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := training.BuildTimeline(c, s)
	return c, s, steps, BuildSessionPackage(c, s, steps)
}

func all(t *testing.T) []SessionPackage {
	t.Helper()
	var out []SessionPackage
	for _, label := range []string{"Upper Body", "Lower Body", "Cardio", "Full Body", "Mobility", "Recovery"} {
		for _, exp := range []training.Experience{training.Beginner, training.Intermediate, training.Advanced} {
			for _, min := range []int{20, 38, 60} {
				_, _, _, pkg := build(t, label, exp, min)
				out = append(out, pkg)
			}
		}
	}
	return out
}

// O pacote tem de ser auto-suficiente: um passo para cada índice, e cada passo
// com tudo o que a interface mostra.
func TestEveryStepIsSelfSufficient(t *testing.T) {
	for _, pkg := range all(t) {
		for i, step := range pkg.Steps {
			if step.Index != i {
				t.Fatalf("%s: passo %d tem índice %d", pkg.SessionID, i, step.Index)
			}
			if step.Dial.Label == "" {
				t.Fatalf("%s passo %d (%s): mostrador sem rótulo", pkg.SessionID, i, step.Kind)
			}
			if step.Action.Label == "" || step.Action.Icon == "" {
				t.Fatalf("%s passo %d: acção incompleta %+v", pkg.SessionID, i, step.Action)
			}
			if step.Kind != string(training.StepDone) && step.Capsule == "" {
				t.Fatalf("%s passo %d (%s): sem exercício na cápsula", pkg.SessionID, i, step.Kind)
			}
		}
	}
}

// O visto é só para séries de trabalho. Aquecer e alongar levam seta.
func TestCheckmarkOnlyForRealWork(t *testing.T) {
	c := training.DefaultConfig()
	for _, pkg := range all(t) {
		_ = c
		for i, step := range pkg.Steps {
			if step.Kind != string(training.StepSet) {
				continue
			}
			isWork := strings.HasPrefix(step.Dial.Label, "SÉRIE")
			if isWork && step.Action.Icon != "checkmark" {
				t.Fatalf("%s passo %d: série de trabalho com ícone %q", pkg.SessionID, i, step.Action.Icon)
			}
			if !isWork && step.Action.Icon == "checkmark" {
				t.Fatalf("%s passo %d (%s): aquecer/alongar com visto — parece já ticado",
					pkg.SessionID, i, step.Dial.Label)
			}
		}
	}
}

// Só o relógio é substituído pelo cliente. Mais nenhum campo tem buracos.
func TestOnlyTheClockIsAPlaceholder(t *testing.T) {
	for _, pkg := range all(t) {
		raw, err := json.Marshal(pkg)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, open := range []string{"{{", "${"} {
			if strings.Contains(text, open) {
				t.Fatalf("%s: o pacote leva um marcador que não é o relógio", pkg.SessionID)
			}
		}
		// E o relógio só aparece onde faz sentido.
		for i, step := range pkg.Steps {
			if step.Dial.Value == clockPlaceholder && step.AutoAdvanceAfterSeconds == nil && step.Kind != string(training.StepSet) {
				t.Fatalf("%s passo %d: mostrador é relógio mas o passo não conta tempo", pkg.SessionID, i)
			}
		}
	}
}

// Uma série a repetições espera pela pessoa; um passo a tempo avança sozinho.
func TestAutoAdvanceOnlyOnTimedSteps(t *testing.T) {
	for _, pkg := range all(t) {
		for i, step := range pkg.Steps {
			switch step.Kind {
			case string(training.StepGetReady), string(training.StepRest), string(training.StepHydrate):
				if step.AutoAdvanceAfterSeconds == nil {
					t.Fatalf("%s passo %d (%s): devia avançar sozinho", pkg.SessionID, i, step.Kind)
				}
			case string(training.StepDone):
				if step.AutoAdvanceAfterSeconds != nil {
					t.Fatalf("%s: o fim não avança sozinho", pkg.SessionID)
				}
			case string(training.StepSet):
				timed := step.Dial.Unit == "segundos"
				if timed != (step.AutoAdvanceAfterSeconds != nil) {
					t.Fatalf("%s passo %d: série a %s com auto-avanço=%v",
						pkg.SessionID, i, step.Dial.Unit, step.AutoAdvanceAfterSeconds != nil)
				}
			}
		}
	}
}

// INVARIANTE 12 — só se remove uma série à frente da posição actual.
//
// O cliente não avalia isto: desenha o menos apagado porque o servidor disse
// `enabled: false`.
func TestInvariant12_RemoveOnlyAhead(t *testing.T) {
	for _, label := range []string{"Upper Body", "Full Body", "Mobility"} {
		for _, exp := range []training.Experience{training.Beginner, training.Advanced} {
			_, s, steps, pkg := build(t, label, exp, 45)

			for i, step := range steps {
				ctrl := pkg.Steps[i].Controls
				at, ok := targetExercise(step)
				if !ok {
					if ctrl.RemoveSet.Enabled {
						t.Fatalf("%s passo %d: sem exercício e a permitir remover", pkg.SessionID, i)
					}
					continue
				}

				ahead := 0
				for k := i + 1; k < len(steps); k++ {
					if steps[k].Kind == training.StepSet && steps[k].ExerciseIndex == at {
						ahead++
					}
				}
				entry := s.Exercises[at]

				want := ahead > 0 && entry.Sets > 1
				if ctrl.RemoveSet.Enabled != want {
					t.Fatalf("%s passo %d (%s, exercício %d, %d séries, %d à frente): remover=%v, devia ser %v",
						pkg.SessionID, i, step.Kind, at, entry.Sets, ahead, ctrl.RemoveSet.Enabled, want)
				}
				if !ctrl.RemoveSet.Enabled && ctrl.RemoveSet.Reason == "" {
					t.Fatalf("%s passo %d: desligado sem razão — a interface não consegue explicar", pkg.SessionID, i)
				}
			}
		}
	}
}

// O progresso das séries nunca recua, e os blocos somam a sessão.
func TestProgressIsCoherentAcrossTheWholePackage(t *testing.T) {
	for _, pkg := range all(t) {
		prev := 0
		for i, step := range pkg.Steps {
			if step.Progress.Sets.Done < prev {
				t.Fatalf("%s passo %d: séries feitas recuaram de %d para %d",
					pkg.SessionID, i, prev, step.Progress.Sets.Done)
			}
			prev = step.Progress.Sets.Done

			if step.Progress.Sets.Total != pkg.TotalSets {
				t.Fatalf("%s passo %d: total de séries %d ≠ %d",
					pkg.SessionID, i, step.Progress.Sets.Total, pkg.TotalSets)
			}

			var weight float64
			for _, b := range step.Progress.Blocks {
				if b.Filled < 0 || b.Filled > 1 {
					t.Fatalf("%s passo %d: bloco %s a %v", pkg.SessionID, i, b.Role, b.Filled)
				}
				weight += b.Weight
			}
			if weight < 0.999 || weight > 1.001 {
				t.Fatalf("%s passo %d: os pesos dos blocos somam %v", pkg.SessionID, i, weight)
			}
		}
		// No fim, todas as séries estão feitas.
		last := pkg.Steps[len(pkg.Steps)-1]
		if last.Progress.Sets.Done != pkg.TotalSets {
			t.Fatalf("%s: no fim, %d de %d séries", pkg.SessionID, last.Progress.Sets.Done, pkg.TotalSets)
		}
	}
}

// O limiar de conclusão vai no pacote — para a interface poder explicar. Mas
// quem decide é o servidor, ao receber os eventos.
func TestCompletionThresholdIsHalfTheSession(t *testing.T) {
	c, _, steps, pkg := build(t, "Full Body", training.Intermediate, 45)
	total := training.RemainingSeconds(c, steps, 0)
	if pkg.CompletionThresholdSeconds != total/2 {
		t.Fatalf("limiar %ds, metade de %ds é %ds", pkg.CompletionThresholdSeconds, total, total/2)
	}
}

// O pacote não leva o score nem nada que o cliente possa usar para decidir.
func TestPackageCarriesNoRuleInputs(t *testing.T) {
	_, _, _, pkg := build(t, "Full Body", training.Intermediate, 45)
	raw, _ := json.Marshal(pkg)
	for _, forbidden := range []string{"\"score\"", "\"setsAhead\"", "\"threshold\":"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("o pacote leva %q — o cliente passaria a poder decidir", forbidden)
		}
	}
}
