package training

import (
	"encoding/json"
	"os"
	"testing"
)

// Paridade com o TypeScript: 819 sessões montadas, 41 520 passos de linha do
// tempo, 60 conjuntos de substituições e 36 listas de exercícios disponíveis.
//
// Cobre os oito rótulos de plano (incluindo um desconhecido, que cai em "full"),
// os três níveis, cinco combinações de equipamento, cinco orçamentos, três dias
// e cinco combinações de fixados/excluídos/prescrições.

type tsSession struct {
	Label string                  `json:"label"`
	Exp   string                  `json:"exp"`
	Eq    []string                `json:"eq"`
	Min   int                     `json:"min"`
	Day   string                  `json:"day"`
	Pin   []string                `json:"pin"`
	Exc   []string                `json:"exc"`
	Pres  map[string]Prescription `json:"pres"`
	S     struct {
		ID               string   `json:"id"`
		Title            string   `json:"title"`
		Focus            string   `json:"focus"`
		Summary          string   `json:"summary"`
		Muscles          []string `json:"muscles"`
		EstimatedSeconds int      `json:"estimatedSeconds"`
		EstimatedKcal    int      `json:"estimatedKcal"`
		Ex               [][]any  `json:"ex"`
	} `json:"s"`
	Steps       [][]any          `json:"steps"`
	Remaining   int              `json:"remaining"`
	TotalSets   int              `json:"totalSets"`
	SetsDoneMid int              `json:"setsDoneMid"`
	Slices      []BlockSlice     `json:"slices"`
	BlockPos    []*BlockPosition `json:"blockPos"`
	Hyd         int              `json:"hyd"`
}

type tsTraining struct {
	Sessions []tsSession `json:"sessions"`
	Subs     []struct {
		ID  string   `json:"id"`
		Exp string   `json:"exp"`
		Eq  []string `json:"eq"`
		R   [][]any  `json:"r"`
	} `json:"subs"`
	Avail []struct {
		Exp string  `json:"exp"`
		Pat *string `json:"pat"`
		Q   *string `json:"q"`
		R   [][]any `json:"r"`
	} `json:"avail"`
}

func loadTS(t *testing.T) tsTraining {
	t.Helper()
	raw, err := os.ReadFile("testdata/ts-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f tsTraining
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func num(v any) int {
	if v == nil {
		return 0
	}
	return int(v.(float64))
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return v.(string)
}

func TestParityBuildSession(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).Sessions
	steps := 0

	for i, tc := range cases {
		got, err := BuildSession(c, BuildInput{
			PlanLabel: tc.Label, Experience: Experience(tc.Exp), Equipment: tc.Eq,
			Minutes: tc.Min, DayISO: tc.Day, Pinned: tc.Pin, Excluded: tc.Exc,
			Prescriptions: tc.Pres,
		})
		if err != nil {
			t.Fatal(err)
		}

		if got.ID != tc.S.ID || got.Title != tc.S.Title || string(got.Focus) != tc.S.Focus ||
			got.Summary != tc.S.Summary || got.EstimatedSeconds != tc.S.EstimatedSeconds ||
			got.EstimatedKcal != tc.S.EstimatedKcal {
			t.Errorf("caso %d (%s %s %dmin):\n Go {%s %q %s %q %ds %dkcal}\n TS {%s %q %s %q %ds %dkcal}",
				i, tc.Label, tc.Exp, tc.Min,
				got.ID, got.Title, got.Focus, got.Summary, got.EstimatedSeconds, got.EstimatedKcal,
				tc.S.ID, tc.S.Title, tc.S.Focus, tc.S.Summary, tc.S.EstimatedSeconds, tc.S.EstimatedKcal)
			continue
		}

		if len(got.Exercises) != len(tc.S.Ex) {
			t.Errorf("caso %d: %d exercícios ≠ %d", i, len(got.Exercises), len(tc.S.Ex))
			continue
		}
		for k, e := range got.Exercises {
			w := tc.S.Ex[k]
			if e.Exercise.ID != str(w[0]) || string(e.Role) != str(w[1]) ||
				e.Sets != num(w[2]) || e.Target != num(w[3]) || e.RestSeconds != num(w[4]) {
				t.Errorf("caso %d exercício %d:\n Go {%s %s %dx%d rest=%d}\n TS {%v %v %vx%v rest=%v}",
					i, k, e.Exercise.ID, e.Role, e.Sets, e.Target, e.RestSeconds, w[0], w[1], w[2], w[3], w[4])
			}
		}

		if len(got.Muscles) != len(tc.S.Muscles) {
			t.Errorf("caso %d: %d músculos ≠ %d — a ordem de aparição importa", i, len(got.Muscles), len(tc.S.Muscles))
		} else {
			for k := range got.Muscles {
				if got.Muscles[k] != tc.S.Muscles[k] {
					t.Errorf("caso %d músculo %d: %q ≠ %q", i, k, got.Muscles[k], tc.S.Muscles[k])
					break
				}
			}
		}

		// ── linha do tempo ───────────────────────────────────────────────────
		timeline := BuildTimeline(c, got)
		steps += len(timeline)
		if len(timeline) != len(tc.Steps) {
			t.Errorf("caso %d: %d passos ≠ %d", i, len(timeline), len(tc.Steps))
			continue
		}
		for k, s := range timeline {
			w := tc.Steps[k]
			if string(s.Kind) != str(w[0]) || s.Seconds != num(w[1]) ||
				s.ExerciseIndex != num(w[2]) || string(s.Role) != str(w[3]) ||
				s.SetNumber != num(w[4]) || s.TotalSets != num(w[5]) ||
				string(s.Measure) != str(w[6]) || s.Target != num(w[7]) ||
				string(s.Reason) != str(w[8]) || s.NextExerciseIndex != num(w[9]) ||
				s.NextSetNumber != num(w[10]) {
				t.Errorf("caso %d passo %d:\n Go %+v\n TS %v", i, k, s, w)
				break
			}
		}

		if r := RemainingSeconds(c, timeline, 0); r != tc.Remaining {
			t.Errorf("caso %d: faltam %ds ≠ %ds", i, r, tc.Remaining)
		}
		if n := TotalSets(timeline); n != tc.TotalSets {
			t.Errorf("caso %d: %d séries ≠ %d", i, n, tc.TotalSets)
		}
		mid := len(timeline) / 2
		if n := SetsDone(timeline, mid); n != tc.SetsDoneMid {
			t.Errorf("caso %d: %d séries feitas a meio ≠ %d", i, n, tc.SetsDoneMid)
		}
		if n := HydrationCount(c, countMain(got)); n != tc.Hyd {
			t.Errorf("caso %d: %d pausas de hidratação ≠ %d", i, n, tc.Hyd)
		}

		slices := BlockSlices(c, got, timeline, mid, 5)
		if len(slices) != len(tc.Slices) {
			t.Errorf("caso %d: %d blocos ≠ %d", i, len(slices), len(tc.Slices))
		} else {
			for k := range slices {
				if slices[k] != tc.Slices[k] {
					t.Errorf("caso %d bloco %d: %+v ≠ %+v", i, k, slices[k], tc.Slices[k])
				}
			}
		}

		for k := range got.Exercises {
			p := BlockPositionOf(got, k)
			w := tc.BlockPos[k]
			if (p == nil) != (w == nil) || (p != nil && *p != *w) {
				t.Errorf("caso %d posição no bloco %d: %+v ≠ %+v", i, k, p, w)
			}
		}
	}
	t.Logf("%d sessões e %d passos iguais ao TypeScript", len(cases), steps)
}

func countMain(s Session) int {
	n := 0
	for _, e := range s.Exercises {
		if e.Role == Main {
			n++
		}
	}
	return n
}

func TestParitySubstitutions(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).Subs
	for i, tc := range cases {
		ex, ok := GetExercise(tc.ID)
		if !ok {
			t.Fatalf("exercício %q não existe na biblioteca", tc.ID)
		}
		got, err := FindSubstitutions(c, SubstituteInput{
			Exercise: ex, Equipment: tc.Eq, Experience: Experience(tc.Exp), Limit: 6,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.R) {
			t.Errorf("caso %d (%s %s): %d opções ≠ %d", i, tc.ID, tc.Exp, len(got), len(tc.R))
			continue
		}
		for k, o := range got {
			w := tc.R[k]
			if o.Exercise.ID != str(w[0]) || o.Sets != num(w[1]) || o.Target != num(w[2]) ||
				o.RestSeconds != num(w[3]) || o.Match != w[4].(float64) {
				t.Errorf("caso %d opção %d:\n Go {%s %dx%d rest=%d match=%v}\n TS %v",
					i, k, o.Exercise.ID, o.Sets, o.Target, o.RestSeconds, o.Match, w)
			}
		}
	}
	t.Logf("%d conjuntos de substituições iguais ao TypeScript", len(cases))
}

func TestParityAvailable(t *testing.T) {
	c := DefaultConfig()
	cases := loadTS(t).Avail
	for i, tc := range cases {
		in := AvailableInput{Equipment: []string{"dumbbells"}, Experience: Experience(tc.Exp)}
		if tc.Pat != nil {
			in.Pattern = MovementPattern(*tc.Pat)
		}
		if tc.Q != nil {
			in.Query = *tc.Q
		}
		got, err := AvailableExercises(c, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tc.R) {
			t.Errorf("caso %d: %d exercícios ≠ %d", i, len(got), len(tc.R))
			continue
		}
		for k, o := range got {
			w := tc.R[k]
			if o.Exercise.ID != str(w[0]) || o.Sets != num(w[1]) || o.Target != num(w[2]) || o.RestSeconds != num(w[3]) {
				t.Errorf("caso %d opção %d: Go {%s %dx%d %d} ≠ TS %v", i, k, o.Exercise.ID, o.Sets, o.Target, o.RestSeconds, w)
			}
		}
	}
	t.Logf("%d listas de exercícios disponíveis iguais ao TypeScript", len(cases))
}
