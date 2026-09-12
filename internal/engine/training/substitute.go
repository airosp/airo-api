package training

import (
	"sort"
	"strings"

	"github.com/airosp/airo-api/internal/engine/portable"
)

type Option struct {
	Exercise Exercise `json:"exercise"`
	// Séries e alvo sugeridos, já traduzidos para este exercício.
	Sets        int `json:"sets"`
	Target      int `json:"target"`
	RestSeconds int `json:"restSeconds"`
	// Match — quanto preserva do que estava lá antes, 0–1. Zero ao acrescentar.
	Match float64 `json:"match,omitempty"`
}

// PrescriptionFor — a prescrição deste exercício: a que a pessoa fixou, ou a
// que o motor daria.
//
// O fixado vem primeiro porque a lista de escolha tem de mostrar os números que
// a pessoa vai mesmo receber. Mostrava 3×12 a quem tinha escrito 4×10, e o
// exercício entrava a 4×10 — a folha e o resultado discordavam.
func PrescriptionFor(c Config, e Exercise, exp Experience, role Role, fixed *Prescription) Option {
	sets := c.SetsByExperience[exp]
	if role != Main {
		sets = 1
	}
	target := c.RepsByPattern[e.Pattern]
	if e.Measure == Time {
		target = c.TimeByPattern[e.Pattern]
	}
	if fixed != nil {
		sets, target = fixed.Sets, fixed.Target
	}
	return Option{
		Exercise: e, Sets: sets, Target: target,
		RestSeconds: int(portable.RoundJS(float64(c.RestByPattern[e.Pattern]) * c.RestFactorByExperience[exp])),
	}
}

type SubstituteInput struct {
	Exercise   Exercise
	Equipment  []string
	Experience Experience
	// Exclude — ids já na sessão. Não se propõe o que já lá está.
	Exclude       []string
	Limit         int
	Prescriptions map[string]Prescription
}

// FindSubstitutions — alternativas para um exercício.
//
// Substituir não é escolher outro exercício qualquer: é **manter a função do
// movimento** dentro da sessão. Um treino de empurrar sem puxar desequilibra o
// ombro, e é o padrão de movimento — não o músculo nem o nome — que sustenta
// esse equilíbrio.
//
//	mesmo padrão de movimento  → 0,60
//	músculos em comum          → 0,25
//	mesma medida (reps/tempo)  → 0,15
func FindSubstitutions(c Config, in SubstituteInput) ([]Option, error) {
	lib, err := Library()
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 6
	}

	rank := levelRank[in.Experience]
	skip := toSet(append([]string{in.Exercise.ID}, in.Exclude...))
	muscles := toSet(in.Exercise.Muscles)

	role := Main
	switch {
	case in.Exercise.Warmup:
		role = Warmup
	case in.Exercise.Cooldown:
		role = Cooldown
	}

	out := []Option{}
	for _, cand := range lib {
		if skip[cand.ID] || !isAvailable(cand, in.Equipment) || levelRank[cand.Level] > rank {
			continue
		}
		// Aquecimento não substitui trabalho, nem o contrário.
		if cand.Warmup != in.Exercise.Warmup || cand.Cooldown != in.Exercise.Cooldown {
			continue
		}

		shared := 0
		for _, m := range cand.Muscles {
			if muscles[m] {
				shared++
			}
		}
		muscleScore := 0.0
		if len(muscles) > 0 {
			muscleScore = float64(shared) / float64(len(muscles))
		}
		match := muscleScore * 0.25
		if cand.Pattern == in.Exercise.Pattern {
			match += 0.60
		}
		if cand.Measure == in.Exercise.Measure {
			match += 0.15
		}

		opt := PrescriptionFor(c, cand, in.Experience, role, prescriptionOf(in.Prescriptions, cand.ID))
		opt.Match = portable.RoundTo(match, 2)
		// Abaixo de 0,4 não é substituição — é outro exercício.
		if opt.Match >= 0.4 {
			out = append(out, opt)
		}
	}

	// Estável: dois candidatos com a mesma compatibilidade mantêm a ordem da
	// biblioteca, como o `sort` do JavaScript faz.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Match > out[j].Match })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type AvailableInput struct {
	Equipment     []string
	Experience    Experience
	Exclude       []string
	Pattern       MovementPattern
	Query         string
	Prescriptions map[string]Prescription
}

// AvailableExercises — o que se pode acrescentar à sessão.
func AvailableExercises(c Config, in AvailableInput) ([]Option, error) {
	lib, err := Library()
	if err != nil {
		return nil, err
	}
	rank := levelRank[in.Experience]
	skip := toSet(in.Exclude)
	term := strings.ToLower(strings.TrimSpace(in.Query))

	out := []Option{}
	for _, cand := range lib {
		if skip[cand.ID] || !isAvailable(cand, in.Equipment) || levelRank[cand.Level] > rank {
			continue
		}
		if in.Pattern != "" && cand.Pattern != in.Pattern {
			continue
		}
		if term != "" && !strings.Contains(strings.ToLower(cand.Name), term) {
			continue
		}
		out = append(out, PrescriptionFor(c, cand, in.Experience, Main, prescriptionOf(in.Prescriptions, cand.ID)))
	}
	return out, nil
}
