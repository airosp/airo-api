package journey

import (
	"testing"
	"testing/quick"
)

// INVARIANTE 15 — adesão ∈ [0,1] com as quatro componentes ponderadas.
//
// Não é uma divisão. `concluídas / planeadas` trataria como iguais quem faz
// três treinos de cinco minutos e quem faz três treinos completos.
func TestInvariant15_AdherenceIsCompositeAndBounded(t *testing.T) {
	c := DefaultConfig()

	f := func(nc, ns uint8, freq, dur uint8, actual uint8) bool {
		if freq == 0 || dur == 0 {
			return true
		}
		act := int(actual)
		sessions := make([]SessionRecord, 0, int(nc)+int(ns))
		for i := 0; i < int(nc); i++ {
			d := time0.AddDate(0, 0, i)
			sessions = append(sessions, SessionRecord{
				OccurredAtISO: d.Format("2006-01-02T15:04:05.000Z"), Status: "completed",
				PlannedDurationMinutes: int(dur), ActualDurationMinutes: &act,
			})
		}
		for i := 0; i < int(ns); i++ {
			d := time0.AddDate(0, 0, i)
			sessions = append(sessions, SessionRecord{
				OccurredAtISO: d.Format("2006-01-02T15:04:05.000Z"), Status: "skipped",
				PlannedDurationMinutes: int(dur),
			})
		}

		a := ComputeAdherence(c, AdherenceInput{
			Sessions:       sessions,
			Plan:           Plan{Frequency: int(freq), SessionDurationMinutes: int(dur), TrainingDays: []int{0, 2, 4}},
			PeriodStartISO: "2026-09-01T00:00:00.000Z",
			PeriodEndISO:   "2026-11-01T00:00:00.000Z",
		})

		for _, v := range []float64{a.Frequency, a.Duration, a.Completion, a.Schedule, a.Score} {
			if v < 0 || v > 1 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 20000}); err != nil {
		t.Fatal(err)
	}
}

// A adesão é composta: duas pessoas com a mesma razão concluídas/planeadas têm
// de pontuar diferente se uma treinou metade do tempo.
func TestAdherenceIsNotADivision(t *testing.T) {
	c := DefaultConfig()
	plan := Plan{Frequency: 3, SessionDurationMinutes: 60, TrainingDays: []int{0, 2, 4}}

	build := func(minutes int) Adherence {
		var sessions []SessionRecord
		m := minutes
		for i := 0; i < 12; i++ {
			d := time0.AddDate(0, 0, i*2)
			sessions = append(sessions, SessionRecord{
				OccurredAtISO: d.Format("2006-01-02T15:04:05.000Z"), Status: "completed",
				PlannedDurationMinutes: 60, ActualDurationMinutes: &m,
			})
		}
		return ComputeAdherence(c, AdherenceInput{
			Sessions: sessions, Plan: plan,
			PeriodStartISO: "2026-09-01T00:00:00.000Z", PeriodEndISO: "2026-10-01T00:00:00.000Z",
		})
	}

	completo := build(60)
	apressado := build(15)

	if completo.Completion != apressado.Completion {
		t.Fatal("a conclusão é a mesma: as duas pessoas fizeram as mesmas sessões")
	}
	if !(completo.Score > apressado.Score) {
		t.Fatalf("quem treinou 60 min pontuou %v e quem treinou 15 pontuou %v — a duração tem de contar",
			completo.Score, apressado.Score)
	}
}

// Uma jornada com prazo reparte-se pela duração pedida: as fases somam o total.
//
// ⚠️ **Não somam em seis durações**, e o porte preserva-o de propósito.
//
// Medido contra o TypeScript de 1 a 104 semanas: 1, 2, 3, 4, 5 e **10** semanas
// dão fases que somam mais do que a jornada — as quatro fases terminam depois
// da data-alvo. A causa é o `max(1, …)` na transição: quando os três
// arredondamentos anteriores já gastaram o total, a transição pede mais uma
// semana que não existe.
//
// As dez semanas são o caso que importa: 70 dias é uma meta plausível, e o
// plano acaba uma semana depois do prazo sem nada a dizê-lo.
//
// Está registado em docs/13-progresso.md (F1). A correcção tem de ser feita nos
// dois lados ao mesmo tempo, senão o servidor e o cliente passam a desenhar
// fases diferentes — por isso não se faz aqui.
//
// Este teste fixa o conjunto: se crescer, alguém alargou o defeito.
func TestPhasesCoverTheWholeJourney(t *testing.T) {
	c := DefaultConfig()
	knownOverflow := map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 10: true}

	for weeks := 1; weeks <= 104; weeks++ {
		start := time0
		end := start.AddDate(0, 0, weeks*7)
		phases := BuildPhases(c, Journey{
			ID: "j", StartDateISO: start.Format("2006-01-02T15:04:05.000Z"),
			TargetDateISO: end.Format("2006-01-02T15:04:05.000Z"),
		})
		sum := 0
		for _, p := range phases {
			if p.Weeks < 1 {
				t.Fatalf("%d semanas: fase %s com %d semanas", weeks, p.Kind, p.Weeks)
			}
			sum += p.Weeks
		}
		switch {
		case knownOverflow[weeks]:
			if sum == weeks {
				t.Fatalf("%d semanas já soma certo — tira-o da lista de defeitos conhecidos", weeks)
			}
		case sum != weeks:
			t.Fatalf("%d semanas: as fases somam %d (defeito novo, fora do conjunto conhecido)", weeks, sum)
		}
		// A adaptação nunca sai do intervalo, seja qual for a duração — e esta
		// vale sempre, incluindo nas seis durações defeituosas.
		if a := phases[0]; a.Weeks < c.AdaptationWeeks.Min || a.Weeks > c.AdaptationWeeks.Max {
			t.Fatalf("%d semanas: adaptação de %d semanas, fora de [%d,%d]",
				weeks, a.Weeks, c.AdaptationWeeks.Min, c.AdaptationWeeks.Max)
		}
	}
}

// Sem prazo não há transição: uma jornada que não acaba não está a caminhar
// para lado nenhum.
func TestOpenEndedHasNoTransition(t *testing.T) {
	phases := BuildPhases(DefaultConfig(), Journey{
		ID: "j", StartDateISO: "2026-09-01T00:00:00.000Z",
	})
	for _, p := range phases {
		if p.Kind == Transition {
			t.Fatal("horizonte aberto não tem transição")
		}
	}
	last := phases[len(phases)-1]
	if last.Kind != Maintenance {
		t.Fatalf("a última fase de um horizonte aberto é manutenção, é %q", last.Kind)
	}
}

// Nada se julga antes de ter havido sessões para cumprir.
func TestNothingJudgedBeforeThereIsAnythingToJudge(t *testing.T) {
	c := DefaultConfig()
	a := ComputeAdherence(c, AdherenceInput{
		Plan:           Plan{Frequency: 3, SessionDurationMinutes: 45, TrainingDays: []int{0, 2, 4}},
		PeriodStartISO: "2026-09-01T00:00:00.000Z",
		PeriodEndISO:   "2026-09-01T12:00:00.000Z", // meio dia de jornada
	})
	if a.Evaluable {
		t.Fatal("meio dia não dá sessões planeadas")
	}
	if r := DetectRisks(c, RiskInput{Adherence: a, NowISO: "x"}); len(r) != 0 {
		t.Fatalf("sem nada a julgar não há riscos: %+v", r)
	}
	d := DecideAdaptation(c, AdaptInput{JourneyID: "j", Adherence: a, NowISO: "x"})
	if d.Kind != Maintain || !d.Applied {
		t.Fatalf("sem nada a julgar, mantém-se: %+v", d)
	}
}

// A Airo propõe, não impõe: uma adaptação que mexe no plano do utilizador nunca
// nasce aplicada.
func TestChangesAreNeverSelfApplied(t *testing.T) {
	c := DefaultConfig()
	weak := Adherence{Score: 0.3, PlannedSessions: 12, CompletedSessions: 3, Evaluable: true}
	d := DecideAdaptation(c, AdaptInput{JourneyID: "j", Adherence: weak, NowISO: "x"})
	if d.Kind == Maintain {
		t.Fatalf("adesão de 30%% devia propor alguma coisa, propôs %q", d.Kind)
	}
	if d.Applied {
		t.Fatalf("%q mexe no plano e nasceu aplicada", d.Kind)
	}
	if d.Reason == "" {
		t.Fatal("uma adaptação sem razão é um plano que muda sem explicação")
	}
}

// Retomar de um volume baixo não é excesso de carga. Sem o piso dos 60 minutos,
// quem volta de 30 para 45 minutos era alertado.
func TestVolumeSpikeHasAFloor(t *testing.T) {
	c := DefaultConfig()
	ad := Adherence{Score: 0.9, PlannedSessions: 12, CompletedSessions: 11, Evaluable: true}

	baixo := DetectRisks(c, RiskInput{Adherence: ad, WeeksElapsed: 4, NowISO: "x",
		ExecutedMinutes: &ExecutedMinutes{Recent: 45, Baseline: 30}})
	for _, r := range baixo {
		if r.Type == VolumeSpike {
			t.Fatal("30 → 45 min não é salto de carga")
		}
	}

	alto := DetectRisks(c, RiskInput{Adherence: ad, WeeksElapsed: 4, NowISO: "x",
		ExecutedMinutes: &ExecutedMinutes{Recent: 180, Baseline: 100}})
	found := false
	for _, r := range alto {
		if r.Type == VolumeSpike {
			found = true
		}
	}
	if !found {
		t.Fatal("100 → 180 min é salto de carga")
	}
}
