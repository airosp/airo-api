package journey

import (
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

const msPerWeek = 7 * 24 * time.Hour

type phaseCopy struct{ Title, Intent string }

var phaseTitles = map[PhaseKind]phaseCopy{
	Adaptation:       {"Adaptação", "Criar o hábito e ensinar o movimento antes de subir a carga."},
	Development:      {"Desenvolvimento", "Construir volume com consistência semana a semana."},
	ProgressionPhase: {"Progressão", "Subir a exigência onde o corpo já está a responder."},
	Transition:       {"Transição", "Consolidar o que foi ganho e preparar o próximo ciclo."},
	Maintenance:      {"Manutenção", "Manter o ritmo sem perder o que já está construído."},
}

func parseISO(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// formatISO escreve como o `toISOString()` do JavaScript: sempre UTC, sempre
// com milissegundos. Sem isto, as datas geradas aqui e as geradas no cliente
// não comparam como texto — e há código que as compara como texto.
func formatISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func addWeeks(fromISO string, weeks int) string {
	t, ok := parseISO(fromISO)
	if !ok {
		return fromISO
	}
	return formatISO(t.Add(time.Duration(weeks) * msPerWeek))
}

// BuildPhases dá forma a uma jornada.
//
// Um plano monolítico de 90 dias ignora que o corpo não responde da mesma
// maneira do princípio ao fim. As fases são o que o reconhece.
//
// **Sem prazo não há fases no sentido habitual**: uma jornada sem fim não está
// a caminhar para lado nenhum, logo não tem "transição". Corre em ciclos.
func BuildPhases(c Config, j Journey) []Phase {
	type entry struct {
		kind  PhaseKind
		weeks int
	}
	var plan []entry

	var totalWeeks int
	if j.TargetDateISO != "" {
		start, okS := parseISO(j.StartDateISO)
		end, okE := parseISO(j.TargetDateISO)
		if okS && okE {
			w := int(portable.RoundJS(float64(end.Sub(start)) / float64(msPerWeek)))
			if w < 1 {
				w = 1
			}
			totalWeeks = w
		}
	}

	if totalWeeks > 0 {
		s := c.PhaseSplit
		adapt := int(portable.RoundJS(float64(totalWeeks) * s.Adaptation))
		if adapt < c.AdaptationWeeks.Min {
			adapt = c.AdaptationWeeks.Min
		}
		if adapt > c.AdaptationWeeks.Max {
			adapt = c.AdaptationWeeks.Max
		}
		dev := maxInt(1, int(portable.RoundJS(float64(totalWeeks)*s.Development)))
		prog := maxInt(1, int(portable.RoundJS(float64(totalWeeks)*s.Progression)))
		// A transição leva o que sobra: é a única forma de as quatro fases
		// somarem exactamente a duração pedida depois de três arredondamentos.
		transition := maxInt(1, totalWeeks-adapt-dev-prog)
		plan = []entry{
			{Adaptation, adapt}, {Development, dev},
			{ProgressionPhase, prog}, {Transition, transition},
		}
	} else {
		cycle := c.OpenEndedCycleWeeks
		plan = []entry{
			{Adaptation, c.AdaptationWeeks.Min + 1},
			{Development, cycle},
			{ProgressionPhase, cycle},
			{Maintenance, cycle},
		}
	}

	cursor := j.StartDateISO
	out := make([]Phase, 0, len(plan))
	for i, e := range plan {
		end := addWeeks(cursor, e.weeks)
		status := Draft
		if i == 0 {
			status = Active
		}
		out = append(out, Phase{
			ID:           fmt.Sprintf("%s-phase-%d", j.ID, i+1),
			JourneyID:    j.ID,
			Kind:         e.kind,
			Index:        i + 1,
			Title:        phaseTitles[e.kind].Title,
			Intent:       phaseTitles[e.kind].Intent,
			StartDateISO: cursor,
			EndDateISO:   end,
			Weeks:        e.weeks,
			Status:       status,
		})
		cursor = end
	}
	return out
}

// CurrentPhase devolve a fase em curso, ou a última quando a jornada já passou
// do fim — nunca nil com fases dadas.
func CurrentPhase(phases []Phase, nowISO string) *Phase {
	now, ok := parseISO(nowISO)
	if !ok || len(phases) == 0 {
		if len(phases) == 0 {
			return nil
		}
		last := phases[len(phases)-1]
		return &last
	}
	for i := range phases {
		start, okS := parseISO(phases[i].StartDateISO)
		end, okE := parseISO(phases[i].EndDateISO)
		if okS && okE && !now.Before(start) && now.Before(end) {
			return &phases[i]
		}
	}
	last := phases[len(phases)-1]
	return &last
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

/*
 * CopyOf é o título e a intenção de uma fase, em português.
 *
 * Exposto porque a tabela `phase` guarda só o género e as datas — o texto é
 * decidido aqui, e uma segunda tabela de títulos no telemóvel era uma tabela
 * que diverge à primeira fase nova.
 */
func CopyOf(k PhaseKind) (title, intent string) {
	c := phaseTitles[k]
	return c.Title, c.Intent
}
