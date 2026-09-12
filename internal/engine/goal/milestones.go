package goal

import (
	"math"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// BuildMilestones transforma uma meta distante numa jornada.
//
// Uma meta a 15 kg é abstracta; a primeira etapa a 3 kg é algo que se cumpre em
// poucas semanas.
func BuildMilestones(c Config, currentKg, targetKg float64) []Milestone {
	delta := targetKg - currentKg
	magnitude := math.Abs(delta)
	step := c.Milestones.StepKg

	target := Milestone{
		Index: 0, WeightKg: portable.RoundTo(targetKg, 1), Progress: 1, IsTarget: true,
	}

	// Uma meta curta não precisa de etapas: já é uma etapa.
	if magnitude <= step*1.5 {
		return []Milestone{target}
	}

	steps := int(portable.RoundJS(magnitude / step))
	if steps < 2 {
		steps = 2
	}
	if steps > c.Milestones.MaxCount {
		steps = c.Milestones.MaxCount
	}

	sign := portable.Sign(delta)
	out := make([]Milestone, 0, steps)
	for i := 1; i <= steps; i++ {
		progress := float64(i) / float64(steps)
		weight := currentKg + sign*(magnitude*progress)
		if i == steps {
			weight = targetKg
		}
		out = append(out, Milestone{
			Index: i,
			// Ao meio quilo: uma etapa a 68,37 kg não é uma etapa, é um número.
			WeightKg: portable.RoundTo(portable.RoundJS(weight*2)/2, 1),
			Progress: portable.RoundTo(progress, 2),
			IsTarget: i == steps,
		})
	}
	return out
}
