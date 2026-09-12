package nutrition

import (
	"math"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

type LogStatus string

const (
	LogPlanned LogStatus = "planned"
	LogEaten   LogStatus = "eaten"
	LogPartial LogStatus = "partial"
	LogSkipped LogStatus = "skipped"
	LogCustom  LogStatus = "custom"
)

type Log struct {
	RecordedAtISO string      `json:"recordedAtISO"`
	Status        LogStatus   `json:"status"`
	Kcal          float64     `json:"kcal"`
	Macros        MacrosFloat `json:"macros"`
	// Portion: 0 é válido — "não comi" não é o mesmo que "não registei".
	Portion float64 `json:"portion"`
}

type Strategy struct {
	CalorieTarget int    `json:"calorieTarget"`
	Macros        Macros `json:"macros"`
	MealsPerDay   int    `json:"mealsPerDay"`
}

type AdherenceResult struct {
	Calories float64 `json:"calories"`
	Protein  float64 `json:"protein"`
	Meals    float64 `json:"meals"`
	Score    float64 `json:"score"`

	LoggedDays int  `json:"loggedDays"`
	Evaluable  bool `json:"evaluable"`

	AverageIntakeKcal *int `json:"averageIntakeKcal"`
	AverageProteinG   *int `json:"averageProteinG"`
}

func ratio(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Min(1, value/total)
}

func parseISO(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// ComputeAdherence — adesão alimentar.
//
// Comer o suficiente conta tanto como não comer a mais, por isso o desvio
// calórico é medido nos **dois** sentidos. A proteína só penaliza em falta:
// comer mais proteína não é incumprimento.
//
// Sem dias registados não há nada a avaliar — e dizê-lo é melhor do que
// inventar uma percentagem.
func ComputeAdherence(logs []Log, strategy Strategy, periodStartISO, periodEndISO string) AdherenceResult {
	start, okS := parseISO(periodStartISO)
	end, okE := parseISO(periodEndISO)
	if !okS || !okE {
		return AdherenceResult{}
	}

	type dayTotals struct {
		kcal, protein float64
		meals         int
	}
	byDay := map[string]*dayTotals{}

	for _, log := range logs {
		if log.Status == LogPlanned || log.Status == LogSkipped {
			continue
		}
		t, ok := parseISO(log.RecordedAtISO)
		if !ok || t.Before(start) || t.After(end) {
			continue
		}
		day := log.RecordedAtISO
		if len(day) > 10 {
			day = day[:10]
		}
		entry, exists := byDay[day]
		if !exists {
			entry = &dayTotals{}
			byDay[day] = entry
		}
		entry.kcal += log.Kcal * log.Portion
		entry.protein += log.Macros.Protein * log.Portion
		entry.meals++
	}

	loggedDays := len(byDay)
	if loggedDays == 0 {
		return AdherenceResult{Evaluable: false}
	}

	var sumKcal, sumProtein float64
	var sumMeals int
	for _, d := range byDay {
		sumKcal += d.kcal
		sumProtein += d.protein
		sumMeals += d.meals
	}

	avgKcal := int(portable.RoundJS(sumKcal / float64(loggedDays)))
	avgProtein := int(portable.RoundJS(sumProtein / float64(loggedDays)))
	avgMeals := float64(sumMeals) / float64(loggedDays)

	// Desvio simétrico: 20% a mais é tão desvio como 20% a menos.
	calories := 1 - math.Min(1, math.Abs(float64(avgKcal-strategy.CalorieTarget))/math.Max(1, float64(strategy.CalorieTarget)))
	protein := ratio(float64(avgProtein), float64(strategy.Macros.Protein))
	meals := ratio(avgMeals, float64(strategy.MealsPerDay))

	elapsedDays := int(portable.RoundJS(float64(end.Sub(start)) / float64(24*time.Hour)))
	if elapsedDays < 1 {
		elapsedDays = 1
	}
	// Registar só três dias em trinta não é 100% de adesão: a cobertura conta.
	coverage := ratio(float64(loggedDays), float64(elapsedDays))

	return AdherenceResult{
		Calories:          portable.RoundTo(calories, 2),
		Protein:           portable.RoundTo(protein, 2),
		Meals:             portable.RoundTo(meals, 2),
		Score:             portable.RoundTo((calories*0.4+protein*0.35+meals*0.25)*(0.5+0.5*coverage), 3),
		LoggedDays:        loggedDays,
		Evaluable:         true,
		AverageIntakeKcal: &avgKcal,
		AverageProteinG:   &avgProtein,
	}
}

// MinLoggedDays — nada se decide antes disto.
func MinLoggedDays(c Config) int { return c.Assessment.MinLoggedDays }
