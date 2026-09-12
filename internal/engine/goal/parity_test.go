package goal

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// Paridade com o TypeScript.
//
// 4121 casos produzidos pela implementação que corre hoje no cliente. Não são
// escritos à mão de propósito: um teste feito a partir da minha leitura do
// código testaria a minha leitura, não o comportamento.

type tsCase struct {
	Input struct {
		Body struct {
			CurrentWeightKg float64  `json:"currentWeightKg"`
			HeightCm        *float64 `json:"heightCm"`
			Age             *int     `json:"age"`
			Sex             *string  `json:"sex"`
		} `json:"body"`
		Goal struct {
			TargetWeightKg *float64 `json:"targetWeightKg"`
			TargetDateISO  *string  `json:"targetDateISO"`
			Priority       *string  `json:"priority"`
		} `json:"goal"`
		Training struct {
			DaysPerWeek     int `json:"daysPerWeek"`
			DurationMinutes int `json:"durationMinutes"`
		} `json:"training"`
		History []struct {
			DateISO  string  `json:"dateISO"`
			WeightKg float64 `json:"weightKg"`
		} `json:"history"`
		NowISO string `json:"nowISO"`
	} `json:"input"`
	Status     string  `json:"status"`
	Scores     Scores  `json:"scores"`
	Signals    [][]any `json:"signals"`
	Recs       [][]any `json:"recs"`
	Milestones [][]any `json:"milestones"`
	Metrics    struct {
		Direction      string       `json:"direction"`
		DeltaKg        float64      `json:"deltaKg"`
		DeltaRatio     float64      `json:"deltaRatio"`
		CurrentBmi     *float64     `json:"currentBmi"`
		TargetBmi      *float64     `json:"targetBmi"`
		RefRange       *WeightRange `json:"refRange"`
		Confidence     string       `json:"confidence"`
		Bmr            *EnergyRange `json:"bmr"`
		Tdee           *EnergyRange `json:"tdee"`
		Factor         float64      `json:"factor"`
		TotalKcal      *int         `json:"totalKcal"`
		DailyKcal      *int         `json:"dailyKcal"`
		WeeklyMinutes  int          `json:"weeklyMinutes"`
		MonthlyMinutes int          `json:"monthlyMinutes"`
		Adequacy       string       `json:"adequacy"`
		Weeks          *int         `json:"weeks"`
		ReqKg          *float64     `json:"reqKg"`
		ReqRatio       *float64     `json:"reqRatio"`
		EstWeeks       *WeeksRange  `json:"estWeeks"`
	} `json:"metrics"`
	Progress *ProgressAssessment `json:"progress"`
}

func (tc tsCase) toInput() Input {
	var in Input
	in.Body.CurrentWeightKg = tc.Input.Body.CurrentWeightKg
	in.Body.HeightCm = tc.Input.Body.HeightCm
	in.Body.Age = tc.Input.Body.Age
	if s := tc.Input.Body.Sex; s != nil {
		sex := Sex(*s)
		in.Body.Sex = &sex
	}
	in.Goal.TargetWeightKg = tc.Input.Goal.TargetWeightKg
	if d := tc.Input.Goal.TargetDateISO; d != nil {
		in.Goal.TargetDateISO = *d
	}
	if p := tc.Input.Goal.Priority; p != nil {
		pr := Priority(*p)
		in.Goal.Priority = &pr
	}
	in.Training.DaysPerWeek = tc.Input.Training.DaysPerWeek
	in.Training.DurationMinutes = tc.Input.Training.DurationMinutes
	for _, h := range tc.Input.History {
		in.History = append(in.History, WeightPoint{DateISO: h.DateISO, WeightKg: h.WeightKg})
	}
	in.NowISO = tc.Input.NowISO
	return in
}

func loadCases(t *testing.T) []tsCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/ts-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []tsCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestParityGoalEngine(t *testing.T) {
	c := DefaultConfig()
	cases := loadCases(t)

	var mismatches int
	fail := func(i int, format string, args ...any) {
		mismatches++
		if mismatches <= 10 {
			t.Errorf("caso %d: "+format, append([]any{i}, args...)...)
		}
	}

	for i, tc := range cases {
		got := Assess(c, tc.toInput())
		m, w := got.Metrics, tc.Metrics

		if string(got.Status) != tc.Status {
			fail(i, "status %q ≠ %q", got.Status, tc.Status)
		}
		if got.Scores != tc.Scores {
			fail(i, "scores %+v ≠ %+v", got.Scores, tc.Scores)
		}
		if string(m.Direction) != w.Direction || m.DeltaKg != w.DeltaKg || !close(m.DeltaRatio, w.DeltaRatio) {
			fail(i, "direcção/delta: %s %.1f %.6f ≠ %s %.1f %.6f",
				m.Direction, m.DeltaKg, m.DeltaRatio, w.Direction, w.DeltaKg, w.DeltaRatio)
		}
		if !eqFloatPtr(m.Body.CurrentBmi, w.CurrentBmi) || !eqFloatPtr(m.Body.TargetBmi, w.TargetBmi) {
			fail(i, "IMC %v/%v ≠ %v/%v", deref(m.Body.CurrentBmi), deref(m.Body.TargetBmi), deref(w.CurrentBmi), deref(w.TargetBmi))
		}
		if !eqRange(m.Body.ReferenceRange, w.RefRange) {
			fail(i, "intervalo de referência %+v ≠ %+v", m.Body.ReferenceRange, w.RefRange)
		}
		if string(m.Body.Confidence) != w.Confidence {
			fail(i, "confiança %q ≠ %q", m.Body.Confidence, w.Confidence)
		}
		if !eqEnergy(m.Energy.BmrKcal, w.Bmr) || !eqEnergy(m.Energy.TdeeKcal, w.Tdee) {
			fail(i, "energia %+v/%+v ≠ %+v/%+v", m.Energy.BmrKcal, m.Energy.TdeeKcal, w.Bmr, w.Tdee)
		}
		if m.Energy.ActivityFactor != w.Factor {
			fail(i, "factor %v ≠ %v", m.Energy.ActivityFactor, w.Factor)
		}
		if !eqIntPtr(m.Energy.TotalKcalEquivalent, w.TotalKcal) || !eqIntPtr(m.Energy.DailyKcalEquivalent, w.DailyKcal) {
			fail(i, "kcal %v/%v ≠ %v/%v", derefI(m.Energy.TotalKcalEquivalent), derefI(m.Energy.DailyKcalEquivalent), derefI(w.TotalKcal), derefI(w.DailyKcal))
		}
		if m.Training.WeeklyMinutes != w.WeeklyMinutes || m.Training.MonthlyMinutes != w.MonthlyMinutes || string(m.Training.Adequacy) != w.Adequacy {
			fail(i, "treino %d/%d/%s ≠ %d/%d/%s", m.Training.WeeklyMinutes, m.Training.MonthlyMinutes, m.Training.Adequacy, w.WeeklyMinutes, w.MonthlyMinutes, w.Adequacy)
		}
		if !eqIntPtr(m.Timeframe.Weeks, w.Weeks) || !eqFloatPtr(m.Timeframe.RequiredWeeklyChangeKg, w.ReqKg) || !eqFloatPtr(m.Timeframe.RequiredWeeklyChangeRatio, w.ReqRatio) {
			fail(i, "prazo %v/%v/%v ≠ %v/%v/%v", derefI(m.Timeframe.Weeks), deref(m.Timeframe.RequiredWeeklyChangeKg), deref(m.Timeframe.RequiredWeeklyChangeRatio), derefI(w.Weeks), deref(w.ReqKg), deref(w.ReqRatio))
		}
		if !eqWeeks(m.Timeframe.EstimatedWeeks, w.EstWeeks) {
			fail(i, "semanas estimadas %+v ≠ %+v", m.Timeframe.EstimatedWeeks, w.EstWeeks)
		}

		// Sinais: mesma ordem, mesmo id, mesma gravidade, mesma mensagem.
		if len(got.Signals) != len(tc.Signals) {
			fail(i, "%d sinais ≠ %d", len(got.Signals), len(tc.Signals))
		} else {
			for k, s := range got.Signals {
				if s.ID != tc.Signals[k][0].(string) || string(s.Severity) != tc.Signals[k][1].(string) || s.Message != tc.Signals[k][2].(string) {
					fail(i, "sinal %d: {%s,%s,%q} ≠ {%v,%v,%q}", k, s.ID, s.Severity, s.Message, tc.Signals[k][0], tc.Signals[k][1], tc.Signals[k][2])
				}
			}
		}

		// Recomendações: mesma ordem e mesma acção.
		if len(got.Recommendations) != len(tc.Recs) {
			fail(i, "%d recomendações ≠ %d", len(got.Recommendations), len(tc.Recs))
		} else {
			for k, r := range got.Recommendations {
				want := tc.Recs[k]
				if r.ID != want[0].(string) || r.Priority != want[1].(string) || string(r.Action.Kind) != want[2].(string) {
					fail(i, "recomendação %d: {%s,%s,%s} ≠ {%v,%v,%v}", k, r.ID, r.Priority, r.Action.Kind, want[0], want[1], want[2])
					continue
				}
				if want[3] != nil {
					if r.Action.TargetWeightKg == nil || *r.Action.TargetWeightKg != want[3].(float64) {
						fail(i, "recomendação %d alvo %v ≠ %v", k, deref(r.Action.TargetWeightKg), want[3])
					}
				}
			}
		}

		if len(got.Milestones) != len(tc.Milestones) {
			fail(i, "%d marcos ≠ %d", len(got.Milestones), len(tc.Milestones))
		} else {
			for k, ms := range got.Milestones {
				want := tc.Milestones[k]
				if ms.Index != int(want[0].(float64)) || ms.WeightKg != want[1].(float64) ||
					ms.Progress != want[2].(float64) || ms.IsTarget != want[3].(bool) {
					fail(i, "marco %d: %+v ≠ %v", k, ms, want)
				}
			}
		}

		if (m.Progress == nil) != (tc.Progress == nil) {
			fail(i, "progresso presente=%v ≠ %v", m.Progress != nil, tc.Progress != nil)
		} else if m.Progress != nil && *m.Progress != *tc.Progress {
			fail(i, "progresso %+v ≠ %+v", *m.Progress, *tc.Progress)
		}
	}

	if mismatches > 0 {
		t.Fatalf("%d divergências em %d casos", mismatches, len(cases))
	}
	t.Logf("%d casos iguais ao TypeScript, campo a campo", len(cases))
}

func close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
func derefI(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
func eqFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return close(*a, *b)
}
func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func eqRange(a, b *WeightRange) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func eqWeeks(a, b *WeeksRange) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func eqEnergy(a, b *EnergyRange) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
