package goal

import (
	"encoding/json"
	"strings"
	"testing"
)

func f(v float64) *float64   { return &v }
func i(v int) *int           { return &v }
func p(v Priority) *Priority { return &v }
func s(v Sex) *Sex           { return &v }

func base() Input {
	return Input{
		Body:     Body{CurrentWeightKg: 80, HeightCm: f(175), Age: i(30), Sex: s(Male)},
		Goal:     GoalInput{TargetWeightKg: f(75)},
		Training: Training{DaysPerWeek: 3, DurationMinutes: 45},
		NowISO:   "2026-09-12T00:00:00Z",
	}
}

// O score é interno. `goalEngineConfig.scoring` diz: "Nunca são mostrados ao
// utilizador". Se um dia alguém lhe tirar a etiqueta `json:"-"`, este teste
// avisa antes de a interface o começar a desenhar.
func TestScoreNeverLeavesTheServer(t *testing.T) {
	a := Assess(DefaultConfig(), base())
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"scores", "overall", "consistency"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("o JSON da avaliação leva %q: %s", forbidden, raw)
		}
	}
	if a.Scores.Overall == 0 {
		t.Fatal("o score continua a ser calculado, só não sai")
	}
}

// Um sinal crítico sobrepõe-se ao score, por melhor que as outras dimensões
// pontuem. Uma meta com um problema grave não sai como "realista".
func TestCriticalAlwaysNeedsReview(t *testing.T) {
	c := DefaultConfig()
	in := base()
	in.Body.CurrentWeightKg = 110
	in.Goal.TargetWeightKg = f(80) // −27% do peso: magnitude_large, crítico

	a := Assess(c, in)
	hasCritical := false
	for _, sig := range a.Signals {
		if sig.Severity == Critical {
			hasCritical = true
		}
	}
	if !hasCritical {
		t.Fatal("esperava um sinal crítico")
	}
	if a.Status != StatusNeedsReview {
		t.Fatalf("com um crítico o estado é %q, devia ser needs_review", a.Status)
	}
}

// A Airo propõe, nunca altera a meta sozinha: qualquer sugestão que mexa no
// alvo vem acompanhada de "Manter a minha meta".
func TestTargetChangeAlwaysOffersKeepGoal(t *testing.T) {
	c := DefaultConfig()
	inputs := []Input{}

	// Conflito de direcção.
	a := base()
	a.Goal.Priority = p(PriorityMuscle)
	a.Goal.TargetWeightKg = f(70)
	inputs = append(inputs, a)

	// Magnitude grande.
	b := base()
	b.Body.CurrentWeightKg = 120
	b.Goal.TargetWeightKg = f(85)
	inputs = append(inputs, b)

	for _, in := range inputs {
		out := Assess(c, in)
		touches, keeps := false, false
		for _, r := range out.Recommendations {
			if r.Action.Kind == ActionSetTarget {
				touches = true
			}
			if r.ID == "keep_goal" {
				keeps = true
			}
		}
		if touches && !keeps {
			t.Fatalf("sugere mexer no alvo sem oferecer manter: %+v", out.Recommendations)
		}
	}
}

// Só faz sentido oferecer mais prazo a quem declarou um prazo.
func TestExtendTimeframeOnlyWithADeadline(t *testing.T) {
	c := DefaultConfig()
	in := base()
	in.Training = Training{DaysPerWeek: 1, DurationMinutes: 10} // treino insuficiente
	in.Goal.TargetWeightKg = f(64)                              // 20% → magnitude grande

	out := Assess(c, in)
	for _, r := range out.Recommendations {
		if r.ID == "extend_timeframe" {
			t.Fatal("ofereceu mais prazo a quem não declarou nenhum")
		}
	}

	in.Goal.TargetDateISO = "2026-11-01"
	out = Assess(c, in)
	found := false
	for _, r := range out.Recommendations {
		if r.ID == "extend_timeframe" {
			found = true
		}
	}
	if !found {
		t.Fatal("com prazo declarado, devia oferecer estendê-lo")
	}
}

// Sem data não se inventa um prazo: devolve-se um intervalo.
func TestNoDateGivesRangeNotPromise(t *testing.T) {
	out := Assess(DefaultConfig(), base())
	if out.Metrics.Timeframe.Weeks != nil {
		t.Fatal("sem data-alvo não há semanas declaradas")
	}
	if out.Metrics.Timeframe.EstimatedWeeks == nil {
		t.Fatal("devia estimar um intervalo")
	}
	r := out.Metrics.Timeframe.EstimatedWeeks
	if r.Min >= r.Max {
		t.Fatalf("o intervalo tem de ser um intervalo: %d–%d", r.Min, r.Max)
	}
}

// Sem sexo declarado o BMR é um intervalo, não um número inventado.
func TestUnknownSexGivesRange(t *testing.T) {
	in := base()
	in.Body.Sex = nil
	m := CalculateMetrics(DefaultConfig(), in)
	if m.Energy.BmrKcal == nil {
		t.Fatal("com altura e idade há BMR")
	}
	if m.Energy.BmrKcal.Low == m.Energy.BmrKcal.High {
		t.Fatal("sem sexo declarado o BMR tem de ser um intervalo")
	}
	if m.Body.Confidence != Medium {
		t.Fatalf("confiança = %q, esperava medium", m.Body.Confidence)
	}
}

// Uma meta curta é uma etapa; não se parte em pedaços o que já é pequeno.
func TestShortGoalIsOneMilestone(t *testing.T) {
	c := DefaultConfig()
	if got := BuildMilestones(c, 72, 70); len(got) != 1 || !got[0].IsTarget {
		t.Fatalf("2 kg devia dar um marco só: %+v", got)
	}
	long := BuildMilestones(c, 95, 75)
	if len(long) != c.Milestones.MaxCount {
		t.Fatalf("20 kg devia dar %d marcos, deu %d", c.Milestones.MaxCount, len(long))
	}
	if last := long[len(long)-1]; last.WeightKg != 75 || !last.IsTarget {
		t.Fatalf("o último marco é a meta: %+v", last)
	}
}

func TestConfigValidation(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := DefaultConfig()
	bad.Scoring.Weights.Body = 0.5 // soma passa a 1,2
	if err := bad.Validate(); err == nil {
		t.Fatal("pesos que não somam 1 deviam ser recusados")
	}
}
