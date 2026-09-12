package journey

import (
	"math"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

const msPerDay = 24 * time.Hour

func ratio(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Min(1, value/total)
}

type AdherenceInput struct {
	Sessions       []SessionRecord
	Plan           Plan
	PeriodStartISO string
	PeriodEndISO   string
	// ExcludedDays são dias que não contam como período de treino: ausências
	// marcadas, pausas. Sem isto, avisar que se vai estar fora sai mais caro do
	// que desaparecer sem dizer nada — exactamente o incentivo errado.
	ExcludedDays int
}

// ComputeAdherence — adesão composta de quatro dimensões.
//
// `concluídas / planeadas` é pobre demais: trataria como iguais quem faz três
// treinos de cinco minutos e quem faz três treinos completos, e quem treina
// sempre fora dos dias combinados e quem cumpre o calendário.
//
//	adesão = frequência×0,40 + duração×0,20 + conclusão×0,25 + horário×0,15
func ComputeAdherence(c Config, in AdherenceInput) Adherence {
	start, okS := parseISO(in.PeriodStartISO)
	end, okE := parseISO(in.PeriodEndISO)
	if !okS || !okE {
		return Adherence{}
	}

	excluded := maxInt(0, in.ExcludedDays)
	elapsed := end.Sub(start) - time.Duration(excluded)*msPerDay
	if elapsed < 0 {
		elapsed = 0
	}
	// Semanas reais, sem mínimo forçado: no primeiro dia não há nada planeado.
	weeks := float64(elapsed) / float64(msPerWeek)
	planned := int(math.Floor(weeks * float64(in.Plan.Frequency)))
	evaluable := planned > 0

	var completed, skipped, onSchedule int
	var actualMinutes float64
	for _, s := range in.Sessions {
		t, ok := parseISO(s.OccurredAtISO)
		if !ok || t.Before(start) || t.After(end) {
			continue
		}
		switch s.Status {
		case "completed":
			completed++
			minutes := float64(s.PlannedDurationMinutes)
			if s.ActualDurationMinutes != nil {
				minutes = float64(*s.ActualDurationMinutes)
			}
			actualMinutes += minutes
			// ⚠️ O dia da semana é lido em UTC.
			//
			// No cliente é `new Date(iso).getDay()`, que usa o fuso do
			// dispositivo: a mesma sessão conta como "no dia combinado" em
			// Maputo e fora dele em Lisboa. É um defeito real do TypeScript, e
			// aqui fixa-se em UTC para o servidor ser determinístico — quando
			// houver fuso do utilizador guardado, passa a ser esse.
			weekday := (int(t.Weekday()) + 6) % 7
			for _, d := range in.Plan.TrainingDays {
				if d == weekday {
					onSchedule++
					break
				}
			}
		case "skipped":
			skipped++
		}
	}

	plannedMinutes := float64(planned * in.Plan.SessionDurationMinutes)

	// As sessões planeadas em que ninguém apareceu contam como falhadas — senão
	// quem treina pouco mas certinho pontua como quem cumpre tudo.
	missed := maxInt(0, planned-completed-skipped)
	accountedFor := maxInt(1, completed+skipped+missed)

	frequency := ratio(float64(completed), float64(planned))
	duration := ratio(actualMinutes, plannedMinutes)
	completion := ratio(float64(completed), float64(accountedFor))
	schedule := ratio(float64(onSchedule), float64(maxInt(1, planned)))

	w := c.AdherenceWeights
	score := portable.RoundTo(
		frequency*w.Frequency+duration*w.Duration+completion*w.Completion+schedule*w.Schedule, 3)

	return Adherence{
		Frequency:  portable.RoundTo(frequency, 2),
		Duration:   portable.RoundTo(duration, 2),
		Completion: portable.RoundTo(completion, 2),
		Schedule:   portable.RoundTo(schedule, 2),
		Score:      score,

		PlannedSessions:   planned,
		CompletedSessions: completed,
		SkippedSessions:   skipped,
		Evaluable:         evaluable,
	}
}

// WeeksSince é a contagem por baixo — quatro dias não são uma semana.
func WeeksSince(fromISO, toISO string) int {
	from, okF := parseISO(fromISO)
	to, okT := parseISO(toISO)
	if !okF || !okT {
		return 0
	}
	w := int(math.Floor(float64(to.Sub(from)) / float64(msPerWeek)))
	if w < 0 {
		return 0
	}
	return w
}
