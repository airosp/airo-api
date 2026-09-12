package goal

import (
	"math"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

func confidenceFrom(b Body) Confidence {
	if b.HeightCm != nil && b.Age != nil && b.Sex != nil && *b.Sex != Unspecified {
		return High
	}
	if b.HeightCm != nil && b.Age != nil {
		return Medium
	}
	return Low
}

// CalculateMetrics é só matemática: nenhuma decisão é tomada aqui.
func CalculateMetrics(c Config, in Input) Metrics {
	nowISO := in.NowISO
	if nowISO == "" {
		nowISO = time.Now().UTC().Format(time.RFC3339)
	}

	current := in.Body.CurrentWeightKg
	target := current
	if in.Goal.TargetWeightKg != nil {
		target = *in.Goal.TargetWeightKg
	}

	direction := DirectionOf(current, target)
	deltaKg := WeightDelta(current, target)
	deltaRatio := WeightDeltaRatio(current, target)

	currentBmi := BodyMassIndex(current, in.Body.HeightCm)
	targetBmi := BodyMassIndex(target, in.Body.HeightCm)

	weeklyMinutes := WeeklyTrainingMinutes(in.Training.DaysPerWeek, in.Training.DurationMinutes)
	factor := ActivityFactorFor(c, weeklyMinutes)
	bmr := BasalMetabolicRate(in.Body)
	tdee := TotalDailyEnergy(bmr, factor)

	var weeks *int
	if in.Goal.TargetDateISO != "" {
		weeks = WeeksBetween(nowISO, in.Goal.TargetDateISO)
	}

	// `weeks ? … : null` em TypeScript: zero semanas é falso, e cai em null.
	// Um prazo que já passou não define ritmo nenhum.
	var requiredWeekly *float64
	if weeks != nil && *weeks != 0 {
		requiredWeekly = WeeklyRateOf(deltaKg, *weeks)
	}

	estimated := EstimatedWeeksRange(c, deltaKg, current, direction)

	// O equivalente diário usa o prazo declarado; sem prazo, o extremo lento do
	// intervalo estimado — serve para a regra do piso calórico ter com que
	// trabalhar.
	var horizonWeeks *int
	switch {
	case weeks != nil:
		horizonWeeks = weeks
	case estimated != nil:
		h := estimated.Max
		horizonWeeks = &h
	}

	var totalKcal, dailyKcal *int
	if direction != MaintainWeight {
		t := EnergyEquivalent(c, deltaKg)
		totalKcal = &t
		if horizonWeeks != nil && *horizonWeeks != 0 && t != 0 {
			d := int(portable.RoundJS(float64(t) / float64(*horizonWeeks*7)))
			dailyKcal = &d
		}
	}

	m := Metrics{
		Direction:       direction,
		CurrentWeightKg: current,
		TargetWeightKg:  target,
		DeltaKg:         deltaKg,
		DeltaRatio:      deltaRatio,
		Body: BodyAssessment{
			CurrentBmi:     currentBmi,
			TargetBmi:      targetBmi,
			ReferenceRange: ReferenceWeightRange(c, in.Body.HeightCm),
			Current:        BodyRangePositionOf(c, currentBmi),
			Target:         BodyRangePositionOf(c, targetBmi),
			Confidence:     confidenceFrom(in.Body),
		},
		Energy: EnergyAssessment{
			BmrKcal: bmr, TdeeKcal: tdee, ActivityFactor: factor,
			TotalKcalEquivalent: totalKcal, DailyKcalEquivalent: dailyKcal,
		},
		Training: TrainingAssessment{
			DaysPerWeek:       in.Training.DaysPerWeek,
			MinutesPerSession: in.Training.DurationMinutes,
			WeeklyMinutes:     weeklyMinutes,
			MonthlyMinutes:    MonthlyTrainingMinutes(weeklyMinutes),
			Adequacy:          TrainingAdequacyOf(c, weeklyMinutes, deltaRatio),
		},
		Timeframe: TimeframeAssessment{
			Weeks:                     weeks,
			RequiredWeeklyChangeKg:    requiredWeekly,
			RequiredWeeklyChangeRatio: WeeklyRateRatio(requiredWeekly, current),
			EstimatedWeeks:            estimated,
		},
	}

	if len(in.History) > 0 {
		planned := requiredWeekly
		if planned == nil && estimated != nil {
			p := math.Abs(deltaKg) / float64(estimated.Max)
			planned = &p
		}
		m.Progress = AssessProgress(c, in.History, target, planned, nowISO)
	}

	return m
}

// Assess é o ponto de entrada: cálculo → regras → pontuação → recomendações.
//
// Puro e síncrono. É o que permite correr a cada movimento da régua no ecrã de
// criar plano sem pedir nada a ninguém.
func Assess(c Config, in Input) Assessment {
	metrics := CalculateMetrics(c, in)
	signals := EvaluateRules(c, in, metrics)
	scores := ScoreGoal(c, signals, len(in.History) > 0)

	return Assessment{
		Status:          StatusFrom(c, scores, signals),
		Metrics:         metrics,
		Signals:         signals,
		Recommendations: BuildRecommendations(c, signals, metrics),
		Milestones:      BuildMilestones(c, metrics.CurrentWeightKg, metrics.TargetWeightKg),
		Scores:          scores,
	}
}
