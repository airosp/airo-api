package journey

import "github.com/airosp/airo-api/internal/engine/portable"

type AdaptInput struct {
	JourneyID string
	Adherence Adherence
	Risks     []Risk
	Trend     *Trend
	// OnPace: nil quando não há previsão. `false` é diferente de desconhecido.
	OnPace              *bool
	MovingTowardsTarget *bool
	NowISO              string
}

// DecideAdaptation — OBSERVAR → AVALIAR → DECIDIR → ADAPTAR.
//
// A decisão guarda sempre a razão. E quando mexe no que o utilizador escolheu,
// fica `Applied: false` até ele aceitar: **a Airo propõe, não impõe.**
//
// A ordem das condições é a decisão: risco alto primeiro, depois "cumpre e não
// responde", depois plateau, depois adesão fraca. Trocá-la muda o que se propõe
// a quem tem dois problemas ao mesmo tempo.
func DecideAdaptation(c Config, in AdaptInput) AdaptationDecision {
	base := AdaptationDecision{
		ID:           in.JourneyID + "-adaptation-" + in.NowISO,
		JourneyID:    in.JourneyID,
		CreatedAtISO: in.NowISO,
		Applied:      false,
	}

	if !in.Adherence.Evaluable {
		base.Kind = Maintain
		base.Reason = "A jornada acabou de começar: ainda não há nada a ajustar."
		base.Applied = true
		return base
	}

	// Risco alto manda em tudo o resto.
	for _, r := range in.Risks {
		if r.Level != LevelHigh {
			continue
		}
		base.Reason = r.Recommendation
		if r.Type == RapidChange {
			// Perder depressa demais não se resolve com menos treino.
			base.Kind = ReviewGoal
			return base
		}
		base.Kind = ReduceLoad
		freq := 1
		if in.Adherence.PlannedSessions > 0 {
			freq = 2
		}
		extra := RecoveryExtra
		base.Changes = PlanChanges{Frequency: &freq, RecoveryStrategy: &extra}
		return base
	}

	// Cumprir o plano e mesmo assim andar para trás não é caso de subir a carga.
	for _, r := range in.Risks {
		if r.Type == NoResponse {
			base.Kind = ReviewGoal
			base.Reason = r.Recommendation
			return base
		}
	}

	for _, r := range in.Risks {
		if r.Type == Plateau {
			high := IntensityHigh
			base.Kind = IncreaseLoad
			base.Reason = "O progresso estabilizou: mudar o estímulo costuma voltar a destravá-lo."
			base.Changes = PlanChanges{Intensity: &high}
			return base
		}
	}

	if in.Adherence.Score < c.Adaptation.WeakAdherence {
		extra := RecoveryExtra
		base.Kind = Simplify
		base.Reason = "A rotina ainda não assentou. Menos exigência agora costuma render mais depois."
		base.Changes = PlanChanges{RecoveryStrategy: &extra}
		return base
	}

	strong := in.Adherence.Score >= c.Adaptation.StrongAdherence
	offPace := in.OnPace != nil && !*in.OnPace

	if strong && offPace {
		// Cumpre tudo e mesmo assim não chega: o problema é o prazo, não a pessoa.
		base.Kind = ExtendTimeframe
		base.Reason = "Estás a cumprir o plano, mas o progresso é mais gradual do que a previsão inicial."
		return base
	}

	trendUncertain := in.Trend != nil && in.Trend.Confidence == Low
	movingAway := in.MovingTowardsTarget != nil && !*in.MovingTowardsTarget
	if strong && !trendUncertain && !offPace && !movingAway {
		high := IntensityHigh
		base.Kind = IncreaseLoad
		base.Reason = "Adesão alta e resposta consistente: dá para subir um pouco a exigência."
		base.Changes = PlanChanges{Intensity: &high}
		return base
	}

	base.Kind = Maintain
	base.Reason = "O plano continua adequado ao que os dados mostram."
	base.Applied = true
	return base
}

// ApplyPhaseToPlan traduz a fase em curso em números de plano.
//
// É aqui que "estás em adaptação" deixa de ser uma etiqueta e passa a ser um
// treino mais curto, menos frequente e com mais recuperação.
func ApplyPhaseToPlan(c Config, kind PhaseKind, base Plan) (Plan, PhasePlan) {
	p, ok := c.PhasePlan[kind]
	if !ok {
		return base, PhasePlan{}
	}
	out := base
	out.Frequency = maxInt(1, base.Frequency+p.FrequencyDelta)
	out.SessionDurationMinutes = int(portable.RoundJS(float64(base.SessionDurationMinutes) * p.DurationFactor))
	return out, p
}
