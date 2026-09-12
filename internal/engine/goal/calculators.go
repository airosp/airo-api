package goal

import (
	"math"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

const msPerWeek = 7 * 24 * time.Hour

// ── Corpo ────────────────────────────────────────────────────────────────────

// BodyMassIndex devolve nil sem altura. O IMC entra como sinal de contexto,
// nunca como veredicto.
func BodyMassIndex(weightKg float64, heightCm *float64) *float64 {
	if heightCm == nil || *heightCm <= 0 || weightKg <= 0 {
		return nil
	}
	m := *heightCm / 100
	bmi := portable.RoundTo(weightKg/(m*m), 2)
	return &bmi
}

// ReferenceWeightRange inverte o IMC para obter o intervalo de peso
// correspondente a uma altura.
func ReferenceWeightRange(c Config, heightCm *float64) *WeightRange {
	if heightCm == nil || *heightCm <= 0 {
		return nil
	}
	squared := math.Pow(*heightCm/100, 2)
	return &WeightRange{
		MinKg: portable.RoundTo(c.BodyRange.LowerBmi*squared, 1),
		MaxKg: portable.RoundTo(c.BodyRange.UpperBmi*squared, 1),
	}
}

func BodyRangePositionOf(c Config, bmi *float64) *RangePosition {
	if bmi == nil {
		return nil
	}
	var p RangePosition
	switch {
	case *bmi < c.BodyRange.LowerBmi:
		p = BelowReference
	case *bmi > c.BodyRange.UpperBmi:
		p = AboveReference
	default:
		p = InReference
	}
	return &p
}

// ── Energia ──────────────────────────────────────────────────────────────────

// BasalMetabolicRate é Mifflin-St Jeor.
//
// Sem sexo declarado devolve um **intervalo** entre as duas fórmulas em vez de
// assumir uma. Assumir seria pior do que admitir a incerteza — e o intervalo
// aparece na interface como intervalo.
func BasalMetabolicRate(b Body) *EnergyRange {
	if b.HeightCm == nil || b.Age == nil || b.CurrentWeightKg <= 0 {
		return nil
	}
	base := 10*b.CurrentWeightKg + 6.25*(*b.HeightCm) - 5*float64(*b.Age)
	male := int(portable.RoundJS(base + 5))
	female := int(portable.RoundJS(base - 161))

	if b.Sex != nil {
		switch *b.Sex {
		case Male:
			return &EnergyRange{Low: male, High: male, Estimate: male}
		case Female:
			return &EnergyRange{Low: female, High: female, Estimate: female}
		}
	}
	return &EnergyRange{
		Low:      female,
		High:     male,
		Estimate: int(portable.RoundJS(float64(female+male) / 2)),
	}
}

// ActivityFactorFor escala com os minutos de treino, não com o número de dias.
func ActivityFactorFor(c Config, weeklyMinutes int) float64 {
	for _, band := range c.Energy.ActivityFactorByWeeklyMinutes {
		if float64(weeklyMinutes) <= band.UpTo {
			return band.Factor
		}
	}
	return 1.2
}

func TotalDailyEnergy(bmr *EnergyRange, factor float64) *EnergyRange {
	if bmr == nil {
		return nil
	}
	return &EnergyRange{
		Low:      int(portable.RoundJS(float64(bmr.Low) * factor)),
		High:     int(portable.RoundJS(float64(bmr.High) * factor)),
		Estimate: int(portable.RoundJS(float64(bmr.Estimate) * factor)),
	}
}

// EnergyEquivalent é o equivalente energético de uma variação de peso.
// Aproximação grosseira, e dita como tal na interface.
func EnergyEquivalent(c Config, deltaKg float64) int {
	return int(portable.RoundJS(math.Abs(deltaKg) * c.Energy.KcalPerKgFat))
}

// ── Prazo ────────────────────────────────────────────────────────────────────

// ParseISO aceita data-só e data-hora, como o `new Date()` do JavaScript:
// "2026-12-29" é meia-noite UTC.
func ParseISO(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func WeeksBetween(fromISO, toISO string) *int {
	from, okF := ParseISO(fromISO)
	to, okT := ParseISO(toISO)
	if !okF || !okT {
		return nil
	}
	w := int(portable.RoundJS(float64(to.Sub(from)) / float64(msPerWeek)))
	if w < 0 {
		w = 0
	}
	return &w
}

// EstimatedWeeksRange é a resposta a "quanto tempo?" quando não há data.
//
// Não se inventa um prazo: devolve-se o intervalo entre o ritmo confortável e o
// máximo recomendado. Um número único seria uma promessa.
func EstimatedWeeksRange(c Config, deltaKg, currentWeightKg float64, dir Direction) *WeeksRange {
	if dir == MaintainWeight || currentWeightKg <= 0 {
		return nil
	}
	rates := c.WeeklyRate.Gain
	if dir == LoseWeight {
		rates = c.WeeklyRate.Loss
	}
	magnitude := math.Abs(deltaKg)
	fastest := currentWeightKg * rates.RecommendedMax
	slowest := currentWeightKg * rates.Comfortable
	if fastest <= 0 || slowest <= 0 {
		return nil
	}
	return &WeeksRange{
		Min: maxInt(1, int(portable.RoundJS(magnitude/fastest))),
		Max: maxInt(1, int(portable.RoundJS(magnitude/slowest))),
	}
}

// ── Treino ───────────────────────────────────────────────────────────────────

func WeeklyTrainingMinutes(daysPerWeek, durationMinutes int) int {
	return maxInt(0, daysPerWeek) * maxInt(0, durationMinutes)
}

func MonthlyTrainingMinutes(weeklyMinutes int) int {
	return int(portable.RoundJS(float64(weeklyMinutes) * 4.33))
}

// TrainingAdequacyOf compara o volume disponível com o que a magnitude da meta
// costuma pedir. Nunca diz "não podes" — diz o que está a limitar.
func TrainingAdequacyOf(c Config, weeklyMinutes int, deltaRatio float64) Adequacy {
	m := c.Training.MinWeeklyMinutes
	required := m.Maintain
	switch {
	case deltaRatio >= c.Magnitude.LargeRatio:
		required = m.LargeChange
	case deltaRatio >= c.Magnitude.NotableRatio:
		required = m.Change
	}
	switch {
	case weeklyMinutes >= required:
		return Sufficient
	case float64(weeklyMinutes) >= float64(required)*0.5:
		return Limited
	default:
		return Insufficient
	}
}

// ── Variação de peso ─────────────────────────────────────────────────────────

func DirectionOf(currentKg, targetKg float64) Direction {
	switch {
	case targetKg < currentKg:
		return LoseWeight
	case targetKg > currentKg:
		return GainWeight
	default:
		return MaintainWeight
	}
}

func WeightDelta(currentKg, targetKg float64) float64 {
	return portable.RoundTo(targetKg-currentKg, 1)
}

func WeightDeltaRatio(currentKg, targetKg float64) float64 {
	if currentKg <= 0 {
		return 0
	}
	return math.Abs(targetKg-currentKg) / currentKg
}

func WeeklyRateOf(deltaKg float64, weeks int) *float64 {
	if weeks <= 0 {
		return nil
	}
	r := portable.RoundTo(math.Abs(deltaKg)/float64(weeks), 3)
	return &r
}

func WeeklyRateRatio(rateKg *float64, currentKg float64) *float64 {
	if rateKg == nil || currentKg <= 0 {
		return nil
	}
	r := *rateKg / currentKg
	return &r
}

// ── Progresso ────────────────────────────────────────────────────────────────

// AssessProgress compara o previsto com o real.
//
// Só existe com histórico suficiente: sem dois registos afastados no tempo não
// há tendência nenhuma a declarar, e declarar uma seria inventar.
func AssessProgress(c Config, history []WeightPoint, targetWeightKg float64, plannedWeeklyRateKg *float64, nowISO string) *ProgressAssessment {
	if len(history) < 2 || plannedWeeklyRateKg == nil || *plannedWeeklyRateKg <= 0 {
		return nil
	}

	ordered := make([]WeightPoint, len(history))
	copy(ordered, history)
	sortByDate(ordered)

	first := ordered[0]
	latest := ordered[len(ordered)-1]

	weeks := WeeksBetween(first.DateISO, nowISO)
	if weeks == nil || *weeks < 1 {
		return nil
	}

	towardsLoss := targetWeightKg < first.WeightKg
	signedRate := *plannedWeeklyRateKg
	if towardsLoss {
		signedRate = -signedRate
	}
	rawExpected := first.WeightKg + signedRate*float64(*weeks)

	// A previsão nunca ultrapassa a própria meta.
	expected := rawExpected
	if towardsLoss {
		expected = math.Max(targetWeightKg, rawExpected)
	} else {
		expected = math.Min(targetWeightKg, rawExpected)
	}

	// Positivo = à frente do previsto, seja qual for a direcção.
	sign := -1.0
	if towardsLoss {
		sign = 1.0
	}
	deviation := portable.RoundTo((expected-latest.WeightKg)*sign, 1)

	tolerance := c.Progress.OnTrackToleranceKg
	trend := OnTrack
	switch {
	case deviation > tolerance:
		trend = Ahead
	case deviation < -tolerance:
		trend = Behind
	}

	return &ProgressAssessment{
		WeeksElapsed:     *weeks,
		StartWeightKg:    first.WeightKg,
		LatestWeightKg:   latest.WeightKg,
		ExpectedWeightKg: portable.RoundTo(expected, 1),
		DeviationKg:      deviation,
		Trend:            trend,
	}
}

func sortByDate(points []WeightPoint) {
	// Ordenação por texto ISO, como o `localeCompare` do TypeScript sobre
	// datas ISO — que é ordem cronológica porque o formato é lexicográfico.
	for i := 1; i < len(points); i++ {
		for j := i; j > 0 && points[j].DateISO < points[j-1].DateISO; j-- {
			points[j], points[j-1] = points[j-1], points[j]
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
