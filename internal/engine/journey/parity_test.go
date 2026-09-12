package journey

import (
	"encoding/json"
	"os"
	"testing"
)

// Paridade com o TypeScript: 7352 casos produzidos pela implementação que corre
// hoje no cliente, cobrindo os seis tipos de risco e as seis adaptações.
//
// Gerados com `TZ=UTC` de propósito — ver a nota sobre fusos em adherence.go.

type tsTrend struct {
	RollingAverage  float64  `json:"rollingAverage"`
	PreviousAverage *float64 `json:"previousAverage"`
	RatePerWeek     *float64 `json:"ratePerWeek"`
	Samples         int      `json:"samples"`
	SpanDays        int      `json:"spanDays"`
	Confidence      string   `json:"confidence"`
}

func (t *tsTrend) toTrend() *Trend {
	if t == nil {
		return nil
	}
	return &Trend{
		RollingAverage: t.RollingAverage, PreviousAverage: t.PreviousAverage,
		RatePerWeek: t.RatePerWeek, Samples: t.Samples, SpanDays: t.SpanDays,
		Confidence: Confidence(t.Confidence),
	}
}

type tsFile struct {
	Phases []struct {
		Start   string  `json:"start"`
		Target  *string `json:"target"`
		Phases  [][]any `json:"phases"`
		Current *string `json:"current"`
	} `json:"phases"`
	Adherence []struct {
		Freq     int       `json:"freq"`
		Dur      int       `json:"dur"`
		Days     []int     `json:"days"`
		Nc       int       `json:"nc"`
		Ns       int       `json:"ns"`
		Act      *int      `json:"act"`
		Excluded int       `json:"excluded"`
		A        Adherence `json:"a"`
	} `json:"adherence"`
	Risks []struct {
		Ad    Adherence `json:"ad"`
		Trend *tsTrend  `json:"trend"`
		Weeks int       `json:"weeks"`
		Ex    *struct {
			Recent   float64 `json:"recent"`
			Baseline float64 `json:"baseline"`
		} `json:"ex"`
		Mtt   *bool    `json:"mtt"`
		Nutri *float64 `json:"nutri"`
		Risks []Risk   `json:"risks"`
	} `json:"risks"`
	Adapt []struct {
		Ad     Adherence `json:"ad"`
		Risks  []Risk    `json:"risks"`
		Trend  *tsTrend  `json:"trend"`
		OnPace *bool     `json:"onPace"`
		Mtt    *bool     `json:"mtt"`
		Adapt  struct {
			ID           string         `json:"id"`
			JourneyID    string         `json:"journeyId"`
			CreatedAtISO string         `json:"createdAtISO"`
			Kind         AdaptationKind `json:"kind"`
			Reason       string         `json:"reason"`
			Changes      PlanChanges    `json:"changes"`
			Applied      bool           `json:"applied"`
		} `json:"adapt"`
	} `json:"adapt"`
}

func load(t *testing.T) tsFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/ts-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f tsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParityPhases(t *testing.T) {
	c := DefaultConfig()
	cases := load(t).Phases
	for i, tc := range cases {
		j := Journey{ID: "j1", GoalID: "g1", StartDateISO: tc.Start}
		if tc.Target != nil {
			j.TargetDateISO = *tc.Target
		}
		got := BuildPhases(c, j)
		if len(got) != len(tc.Phases) {
			t.Fatalf("caso %d: %d fases ≠ %d", i, len(got), len(tc.Phases))
		}
		for k, p := range got {
			w := tc.Phases[k]
			if p.ID != w[0].(string) || string(p.Kind) != w[1].(string) ||
				p.Index != int(w[2].(float64)) || p.Title != w[3].(string) ||
				p.StartDateISO != w[4].(string) || p.EndDateISO != w[5].(string) ||
				p.Weeks != int(w[6].(float64)) || string(p.Status) != w[7].(string) {
				t.Errorf("caso %d fase %d:\n Go %+v\n TS %v", i, k, p, w)
			}
		}
		cur := CurrentPhase(got, "2026-10-01T00:00:00.000Z")
		if tc.Current == nil {
			if cur != nil {
				t.Errorf("caso %d: fase actual %v, TS deu null", i, cur.Kind)
			}
		} else if cur == nil || string(cur.Kind) != *tc.Current {
			t.Errorf("caso %d: fase actual %v ≠ %q", i, cur, *tc.Current)
		}
	}
	t.Logf("%d jornadas com fases iguais ao TypeScript", len(cases))
}

func TestParityAdherence(t *testing.T) {
	c := DefaultConfig()
	cases := load(t).Adherence
	for i, tc := range cases {
		mk := func(n int, status string, offsetDays int, act *int, planned int) []SessionRecord {
			out := make([]SessionRecord, 0, n)
			for k := 0; k < n; k++ {
				day := time0.AddDate(0, 0, offsetDays+k*2)
				out = append(out, SessionRecord{
					OccurredAtISO:          day.Format("2006-01-02T15:04:05.000Z"),
					Status:                 status,
					PlannedDurationMinutes: planned,
					ActualDurationMinutes:  act,
				})
			}
			return out
		}
		sessions := append(mk(tc.Nc, "completed", 0, tc.Act, tc.Dur), mk(tc.Ns, "skipped", 1, nil, tc.Dur)...)
		got := ComputeAdherence(c, AdherenceInput{
			Sessions:       sessions,
			Plan:           Plan{Frequency: tc.Freq, SessionDurationMinutes: tc.Dur, TrainingDays: tc.Days},
			PeriodStartISO: "2026-09-01T00:00:00.000Z",
			PeriodEndISO:   "2026-10-15T00:00:00.000Z",
			ExcludedDays:   tc.Excluded,
		})
		if got != tc.A {
			t.Errorf("caso %d (freq=%d dur=%d dias=%v nc=%d ns=%d exc=%d):\n Go %+v\n TS %+v",
				i, tc.Freq, tc.Dur, tc.Days, tc.Nc, tc.Ns, tc.Excluded, got, tc.A)
		}
	}
	t.Logf("%d casos de adesão iguais ao TypeScript", len(cases))
}

func TestParityRisks(t *testing.T) {
	c := DefaultConfig()
	cases := load(t).Risks
	for i, tc := range cases {
		in := RiskInput{
			Adherence: tc.Ad, Trend: tc.Trend.toTrend(), BodyWeightKg: 78,
			WeeksElapsed: tc.Weeks, MovingTowardsTarget: tc.Mtt,
			NutritionAdherence: tc.Nutri, NowISO: "2026-10-15T00:00:00.000Z",
		}
		if tc.Ex != nil {
			in.ExecutedMinutes = &ExecutedMinutes{Recent: tc.Ex.Recent, Baseline: tc.Ex.Baseline}
		}
		got := DetectRisks(c, in)
		if len(got) != len(tc.Risks) {
			t.Fatalf("caso %d: %d riscos ≠ %d\n Go %+v\n TS %+v", i, len(got), len(tc.Risks), got, tc.Risks)
		}
		for k, r := range got {
			w := tc.Risks[k]
			if r.ID != w.ID || r.Type != w.Type || r.Level != w.Level || r.Confidence != w.Confidence ||
				r.Recommendation != w.Recommendation || len(r.Evidence) != len(w.Evidence) {
				t.Errorf("caso %d risco %d:\n Go %+v\n TS %+v", i, k, r, w)
				continue
			}
			for e := range r.Evidence {
				if r.Evidence[e] != w.Evidence[e] {
					t.Errorf("caso %d risco %d evidência %d: %q ≠ %q", i, k, e, r.Evidence[e], w.Evidence[e])
				}
			}
		}
	}
	t.Logf("%d casos de risco iguais ao TypeScript", len(cases))
}

func TestParityAdaptation(t *testing.T) {
	c := DefaultConfig()
	cases := load(t).Adapt
	for i, tc := range cases {
		got := DecideAdaptation(c, AdaptInput{
			JourneyID: "j1", Adherence: tc.Ad, Risks: tc.Risks, Trend: tc.Trend.toTrend(),
			OnPace: tc.OnPace, MovingTowardsTarget: tc.Mtt, NowISO: "2026-10-15T00:00:00.000Z",
		})
		w := tc.Adapt
		if got.ID != w.ID || got.Kind != w.Kind || got.Reason != w.Reason || got.Applied != w.Applied {
			t.Errorf("caso %d:\n Go {%s %s %q applied=%v}\n TS {%s %s %q applied=%v}",
				i, got.ID, got.Kind, got.Reason, got.Applied, w.ID, w.Kind, w.Reason, w.Applied)
			continue
		}
		if !sameChanges(got.Changes, w.Changes) {
			t.Errorf("caso %d mudanças:\n Go %+v\n TS %+v", i, got.Changes, w.Changes)
		}
	}
	t.Logf("%d casos de adaptação iguais ao TypeScript", len(cases))
}

func sameChanges(a, b PlanChanges) bool {
	eqI := func(x, y *int) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	eqIn := func(x, y *Intensity) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	eqR := func(x, y *Recovery) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	return eqI(a.Frequency, b.Frequency) && eqI(a.SessionDurationMinutes, b.SessionDurationMinutes) &&
		eqIn(a.Intensity, b.Intensity) && eqR(a.RecoveryStrategy, b.RecoveryStrategy)
}
