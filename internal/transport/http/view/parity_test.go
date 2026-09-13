package view_test

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/transport/http/view"
)

// Paridade do pacote da sessão com o TypeScript: 1440 pacotes, 63 977 passos.
//
// O Modo Foco desenha `SessionStep` e mais nada. Esse pacote é montado em dois
// sítios — aqui e em `mobile/lib/session-package/from-engine.ts` — porque a app
// tem de funcionar com o pacote do servidor e sem ele. Dois sítios a montar a
// mesma coisa é uma divergência à espera de acontecer, e a divergência não seria
// um erro: seria a app a dizer "Série feita" onde o servidor diz "Já aqueci".
//
// Os casos são gerados correndo o **TypeScript verdadeiro** compilado, não uma
// reimplementação. É o que torna isto uma comparação e não duas suposições.
type tsPackageCase struct {
	Label string   `json:"label"`
	Exp   string   `json:"exp"`
	Eq    []string `json:"eq"`
	Min   int      `json:"min"`
	Day   string   `json:"day"`
	Pkg   struct {
		SessionID                  string      `json:"sessionId"`
		Title                      string      `json:"title"`
		Summary                    string      `json:"summary"`
		EstimatedSeconds           int         `json:"estimatedSeconds"`
		EstimatedKcal              int         `json:"estimatedKcal"`
		TotalSets                  int         `json:"totalSets"`
		CompletionThresholdSeconds int         `json:"completionThresholdSeconds"`
		Steps                      []view.Step `json:"steps"`
	} `json:"pkg"`
}

func TestSessionPackageParityWithTypeScript(t *testing.T) {
	// Comprimido: em JSON estes casos são 36 MB. A cobertura vale mais do que
	// o formato, e ler gzip não custa dependência nenhuma.
	f, err := os.Open("testdata/ts-packages.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()

	var file struct {
		Cases []tsPackageCase `json:"cases"`
	}
	if err := json.NewDecoder(gz).Decode(&file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("sem casos")
	}

	c := training.DefaultConfig()
	steps := 0

	for _, want := range file.Cases {
		name := fmt.Sprintf("%s/%s/%dmin/%s", want.Label, want.Exp, want.Min, want.Day)

		session, err := training.BuildSession(c, training.BuildInput{
			PlanLabel:  want.Label,
			Experience: training.Experience(want.Exp),
			Equipment:  want.Eq,
			Minutes:    want.Min,
			DayISO:     want.Day,
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := view.BuildSessionPackage(c, session, training.BuildTimeline(c, session))

		if got.SessionID != want.Pkg.SessionID {
			t.Errorf("%s: sessionId %q ≠ %q", name, got.SessionID, want.Pkg.SessionID)
		}
		if got.Title != want.Pkg.Title || got.Summary != want.Pkg.Summary {
			t.Errorf("%s: título/resumo %q/%q ≠ %q/%q",
				name, got.Title, got.Summary, want.Pkg.Title, want.Pkg.Summary)
		}
		if got.TotalSets != want.Pkg.TotalSets {
			t.Errorf("%s: totalSets %d ≠ %d", name, got.TotalSets, want.Pkg.TotalSets)
		}
		if got.CompletionThresholdSeconds != want.Pkg.CompletionThresholdSeconds {
			t.Errorf("%s: limiar %d ≠ %d",
				name, got.CompletionThresholdSeconds, want.Pkg.CompletionThresholdSeconds)
		}
		if len(got.Steps) != len(want.Pkg.Steps) {
			t.Errorf("%s: %d passos ≠ %d", name, len(got.Steps), len(want.Pkg.Steps))
			continue
		}

		for i := range got.Steps {
			compareStep(t, name, i, got.Steps[i], want.Pkg.Steps[i])
			steps++
		}
	}
	t.Logf("%d pacotes, %d passos comparados campo a campo", len(file.Cases), steps)
}

func compareStep(t *testing.T, name string, i int, got, want view.Step) {
	t.Helper()
	where := fmt.Sprintf("%s passo %d (%s)", name, i, got.Kind)

	if got.Kind != want.Kind {
		t.Errorf("%s: kind %q ≠ %q", where, got.Kind, want.Kind)
	}
	// O mostrador é o que a pessoa lê em grande. Uma diferença aqui é a app a
	// dizer outra coisa conforme haja rede.
	if got.Dial != want.Dial {
		t.Errorf("%s: dial %+v ≠ %+v", where, got.Dial, want.Dial)
	}
	if got.Pill != want.Pill {
		t.Errorf("%s: pill %q ≠ %q", where, got.Pill, want.Pill)
	}
	if got.Capsule != want.Capsule {
		t.Errorf("%s: capsule %q ≠ %q", where, got.Capsule, want.Capsule)
	}
	if got.Cue != want.Cue {
		t.Errorf("%s: cue %q ≠ %q", where, got.Cue, want.Cue)
	}
	if got.Action != want.Action {
		t.Errorf("%s: action %+v ≠ %+v", where, got.Action, want.Action)
	}
	if got.Tone != want.Tone {
		t.Errorf("%s: tone %q ≠ %q", where, got.Tone, want.Tone)
	}
	if !eqIntPtr(got.AutoAdvanceAfterSeconds, want.AutoAdvanceAfterSeconds) {
		t.Errorf("%s: autoAdvance %v ≠ %v",
			where, showIntPtr(got.AutoAdvanceAfterSeconds), showIntPtr(want.AutoAdvanceAfterSeconds))
	}
	if got.Progress.Sets != want.Progress.Sets {
		t.Errorf("%s: séries %+v ≠ %+v", where, got.Progress.Sets, want.Progress.Sets)
	}
	if got.Controls != want.Controls {
		t.Errorf("%s: controlos %+v ≠ %+v", where, got.Controls, want.Controls)
	}
	if len(got.Muscles) != len(want.Muscles) {
		t.Errorf("%s: %d músculos ≠ %d", where, len(got.Muscles), len(want.Muscles))
	} else {
		for k := range got.Muscles {
			if got.Muscles[k] != want.Muscles[k] {
				t.Errorf("%s: músculo %d %q ≠ %q", where, k, got.Muscles[k], want.Muscles[k])
			}
		}
	}
	if len(got.Progress.Blocks) != len(want.Progress.Blocks) {
		t.Errorf("%s: %d blocos ≠ %d", where, len(got.Progress.Blocks), len(want.Progress.Blocks))
		return
	}
	for k := range got.Progress.Blocks {
		g, w := got.Progress.Blocks[k], want.Progress.Blocks[k]
		if g.Role != w.Role || !nearly(g.Weight, w.Weight) || !nearly(g.Filled, w.Filled) {
			t.Errorf("%s: bloco %d %+v ≠ %+v", where, k, g, w)
		}
	}
}

// As fracções passam por JSON e por duas linguagens. Meio milésimo de tolerância
// cobre a representação sem esconder uma repartição diferente.
func nearly(a, b float64) bool {
	d := a - b
	return d < 0.0005 && d > -0.0005
}

func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func showIntPtr(v *int) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprint(*v)
}
