package view

import (
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
)

// ── A semana ─────────────────────────────────────────────────────────────────
//
// Substitui o `buildWeeklyPlan` do telemóvel. O que o cliente desenhava a
// partir de `workoutDays` — rótulos, dia activo, minutos — vem decidido, e com
// a precedência do calendário já aplicada:
//
//	excepção do dia  >  ausência  >  padrão semanal
//
// Quem marcou "treino" a meio de uma viagem quis mesmo dizer isso, e essa regra
// não pode viver em dois sítios.

type WeekDay struct {
	// ID é o dia em ISO, que é o que o ecrã usa como chave.
	ID string `json:"id"`
	// Weekday em português curto: SEG, TER…
	Weekday string `json:"weekday"`
	// Date é o dia do mês, com dois dígitos.
	Date string `json:"date"`
	// Label é "Tronco", "Pernas", "Recuperação" — já traduzido.
	Label   string `json:"label"`
	Focus   string `json:"focus"`
	Minutes int    `json:"minutes"`
	// Training distingue um dia de treino de um de recuperação sem o ecrã ter
	// de comparar rótulos.
	Training bool `json:"training"`
	Active   bool `json:"active"`
	Done     bool `json:"done"`
	// Absent quando há ausência marcada. O ecrã mostra-o de outra maneira: um
	// dia em que a pessoa avisou que ia estar fora não é um dia falhado.
	Absent bool `json:"absent,omitempty"`
	// Override diz que a marca do dia ganhou ao padrão da semana.
	Override string `json:"override,omitempty"`
	Note     string `json:"note,omitempty"`
}

type Week struct {
	// Start é a segunda-feira da semana.
	Start string    `json:"start"`
	Days  []WeekDay `json:"days"`
	// Summary é a semana numa frase: "3 treinos · 135 min".
	Summary string `json:"summary"`
}

var diasCurtos = []string{"SEG", "TER", "QUA", "QUI", "SEX", "SÁB", "DOM"}

// WeekInput é o que a semana precisa e o pedido não traz.
type WeekInput struct {
	Start           time.Time
	Today           time.Time
	WorkoutDays     []int
	WorkoutMinutes  int
	RecoveryMinutes int
	// Overrides: dia ISO → "workout" | "rest". Ganham ao padrão.
	Overrides map[string]string
	// Absences: dias ISO marcados como ausência.
	Absences map[string]bool
	Notes    map[string]string
	// Done: dias ISO com treino já concluído.
	Done map[string]bool
	// FocusOf traduz o rótulo do plano no foco, para o ecrã não o deduzir.
	FocusOf func(label string) string
	// TitleOf é o rótulo em português.
	TitleOf func(label string) string
}

func BuildWeek(in WeekInput) Week {
	out := Week{Start: in.Start.Format("2006-01-02"), Days: make([]WeekDay, 0, 7)}
	hoje := in.Today.Format("2006-01-02")

	treinos, minutos := 0, 0
	for i := 0; i < 7; i++ {
		data := in.Start.AddDate(0, 0, i)
		iso := data.Format("2006-01-02")

		// A precedência, num sítio só.
		treina := contemDia(in.WorkoutDays, i)
		ausente := in.Absences[iso]
		override := in.Overrides[iso]
		switch override {
		case "workout":
			treina = true
		case "rest":
			treina = false
		default:
			// Sem excepção explícita, a ausência manda: quem avisou que ia
			// estar fora não tem um treino marcado nesse dia.
			if ausente {
				treina = false
			}
		}

		label := training.PlanLabelOn(in.WorkoutDays, data)
		if !treina {
			label = training.RecoveryLabel
		} else if override == "workout" && label == training.RecoveryLabel {
			// Um treino acrescentado a um dia de descanso não tem lugar na
			// rotação; fica corpo inteiro, que é o que serve para qualquer dia.
			label = "Full Body"
		}

		dia := WeekDay{
			ID: iso, Weekday: diasCurtos[i], Date: data.Format("02"),
			Label: in.TitleOf(label), Focus: in.FocusOf(label),
			Training: treina, Active: iso == hoje,
			Done: in.Done[iso], Absent: ausente,
			Override: override, Note: in.Notes[iso],
		}
		dia.Minutes = in.RecoveryMinutes
		if treina {
			dia.Minutes = in.WorkoutMinutes
			treinos++
			minutos += in.WorkoutMinutes
		}
		out.Days = append(out.Days, dia)
	}

	out.Summary = plural(treinos) + " treinos · " + plural(minutos) + " min"
	if treinos == 1 {
		out.Summary = "1 treino · " + plural(minutos) + " min"
	}
	return out
}

func contemDia(dias []int, i int) bool {
	for _, d := range dias {
		if d == i {
			return true
		}
	}
	return false
}
