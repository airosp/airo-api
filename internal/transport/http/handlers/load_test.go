package handlers

import (
	"encoding/json"
	"testing"

	"github.com/airosp/airo-api/internal/domain"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/dto"
)

func prescricao(slug string, series int) repo.PrescriptionRow {
	p := repo.PrescriptionRow{ExerciseSlug: slug, Sets: series, Target: domain.Reps(10)}
	for i := 0; i < series; i++ {
		p.Detail = append(p.Detail, repo.SetRow{Index: i, Target: domain.Reps(10)})
	}
	return p
}

func kg(v float64) *float64 { return &v }
func n(v int) *int          { return &v }

/*
 * A carga entra por cima do que foi pedido, série a série.
 *
 * O `target` é do motor e o `actual` é da pessoa: duas colunas porque são duas
 * coisas. Adaptar o plano amanhã não pode reescrever o que foi feito ontem.
 */
func TestACargaEntraSerieASerie(t *testing.T) {
	prescricoes := []repo.PrescriptionRow{prescricao("back_squat", 4)}

	aplicarFeito(prescricoes, []dto.PerformedExercise{{
		ExerciseID: "back_squat",
		Sets: []dto.PerformedSet{
			{Index: 0, Reps: n(10), WeightKg: kg(40), Completed: true},
			{Index: 1, Reps: n(8), WeightKg: kg(42.5), Completed: true},
		},
	}})

	detalhe := prescricoes[0].Detail
	if detalhe[0].Actual == nil || detalhe[1].Actual == nil {
		t.Fatal("as séries feitas ficaram sem `actual`")
	}
	if detalhe[0].Actual.Type != domain.MetricLoadReps {
		t.Errorf("primeira série ficou %q, esperava load_reps", detalhe[0].Actual.Type)
	}
	if *detalhe[0].Actual.WeightKg != 40 || *detalhe[1].Actual.WeightKg != 42.5 {
		t.Error("a carga por série perdeu-se")
	}
	if !detalhe[0].Completed || !detalhe[1].Completed {
		t.Error("séries feitas ficaram por completas")
	}

	// ⚠️ As que ninguém mandou ficam sem `actual`. Copiar o alvo para lá era
	// inventar que toda a gente cumpre o plano à letra.
	if detalhe[2].Actual != nil || detalhe[3].Actual != nil {
		t.Error("séries que ninguém fez ganharam um `actual`")
	}
}

// A forma sai da combinação que veio, da mais específica para a mais simples.
func TestAFormaSaiDoQueVeio(t *testing.T) {
	casos := []struct {
		nome     string
		set      dto.PerformedSet
		esperado domain.MetricType
		ok       bool
	}{
		{"repetições com peso", dto.PerformedSet{Reps: n(10), WeightKg: kg(40)}, domain.MetricLoadReps, true},
		{"só repetições", dto.PerformedSet{Reps: n(12)}, domain.MetricReps, true},
		{"tempo", dto.PerformedSet{DurationSeconds: n(45)}, domain.MetricTime, true},
		{"distância", dto.PerformedSet{DistanceMeters: n(400)}, domain.MetricDistance, true},
		{"nada", dto.PerformedSet{}, "", false},
	}
	for _, c := range casos {
		m, ok := metricaDoFeito(c.set)
		if ok != c.ok {
			t.Errorf("%s: aceite=%v, esperava %v", c.nome, ok, c.ok)
			continue
		}
		if ok && m.Type != c.esperado {
			t.Errorf("%s: deu %q, esperava %q", c.nome, m.Type, c.esperado)
		}
	}
}

// Um exercício que não estava no plano não rebenta nada — cai em silêncio.
func TestExercicioForaDoPlanoNaoRebenta(t *testing.T) {
	prescricoes := []repo.PrescriptionRow{prescricao("pushup", 2)}
	aplicarFeito(prescricoes, []dto.PerformedExercise{
		{ExerciseID: "exercicio_que_nao_existe", Sets: []dto.PerformedSet{{Index: 0, Reps: n(10)}}},
		// E um índice fora do que foi pedido também não.
		{ExerciseID: "pushup", Sets: []dto.PerformedSet{{Index: 9, Reps: n(10)}}},
	})
	for _, s := range prescricoes[0].Detail {
		if s.Actual != nil {
			t.Error("escreveu onde não devia")
		}
	}
}

// O `actual` vai para a base como JSON com a forma que o esquema documenta.
func TestOActualSerializaComoOEsquemaDiz(t *testing.T) {
	prescricoes := []repo.PrescriptionRow{prescricao("deadlift", 1)}
	aplicarFeito(prescricoes, []dto.PerformedExercise{{
		ExerciseID: "deadlift",
		Sets:       []dto.PerformedSet{{Index: 0, Reps: n(5), WeightKg: kg(80), Completed: true}},
	}})

	b, err := json.Marshal(prescricoes[0].Detail[0].Actual)
	if err != nil {
		t.Fatal(err)
	}
	var forma struct {
		Type     string  `json:"type"`
		Reps     int     `json:"reps"`
		WeightKg float64 `json:"weightKg"`
	}
	if err := json.Unmarshal(b, &forma); err != nil {
		t.Fatal(err)
	}
	if forma.Type != "load_reps" || forma.Reps != 5 || forma.WeightKg != 80 {
		t.Errorf("forma gravada: %s", b)
	}
}

// A frase sai em português: vírgula decimal, e sem zeros a mais.
func TestAFraseSaiEmPortugues(t *testing.T) {
	casos := map[string]domain.Metric{
		"10 × 40 kg":  domain.LoadReps(10, 40),
		"8 × 42,5 kg": domain.LoadReps(8, 42.5),
		"12 reps":     domain.Reps(12),
		"45 s":        domain.Seconds(45),
		"400 m":       domain.Meters(400),
	}
	for esperado, m := range casos {
		if got := porExtenso(m); got != esperado {
			t.Errorf("deu %q, esperava %q", got, esperado)
		}
	}
}
