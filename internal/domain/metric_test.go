package domain

import (
	"encoding/json"
	"testing"
)

func TestVolumeByType(t *testing.T) {
	cases := []struct {
		name string
		m    Metric
		want float64
	}{
		{"repetições", Reps(12), 12},
		{"carga × repetições", LoadReps(10, 40), 400},
		{"tempo a 3 s por repetição", Seconds(45), 15},
		{"distância a 100 m por unidade", Meters(400), 4},
		{"tipo sem o seu campo dá zero", Metric{Type: MetricReps}, 0},
	}
	for _, tc := range cases {
		if got := tc.m.Volume(); got != tc.want {
			t.Errorf("%s: volume = %v, queria %v", tc.name, got, tc.want)
		}
	}
}

// A progressão depende de o volume distinguir estes dois casos. É a razão de
// ser da tabela `exercise_set` — ver docs/backend/06-lacunas.md, Lacuna 2.
func TestVolumeDistinguishesProgressFromRegression(t *testing.T) {
	semana1 := LoadReps(10, 20)
	semana3 := LoadReps(10, 25)
	if !(semana3.Volume() > semana1.Volume()) {
		t.Fatal("3×10 @ 25 kg tem de contar mais do que 3×10 @ 20 kg")
	}

	antes := Reps(12)
	depois := Reps(8)
	if !(depois.Volume() < antes.Volume()) {
		t.Fatal("2×8 tem de contar menos do que 3×12")
	}
}

func TestZeroIsNotAbsent(t *testing.T) {
	// "Tentei e falhei" é um registo; "não medi" é outro. Um `int` simples
	// colapsava os dois em 0.
	tentou := Reps(0)
	if tentou.Reps == nil {
		t.Fatal("reps = 0 tem de ficar registado, não ausente")
	}
	naoMediu := Metric{Type: MetricReps}
	if naoMediu.Reps != nil {
		t.Fatal("ausente tem de ser nil")
	}
	if err := tentou.Validate(); err != nil {
		t.Fatalf("reps = 0 é válido: %v", err)
	}
	if err := naoMediu.Validate(); err == nil {
		t.Fatal("reps ausente devia falhar a validação")
	}
}

func TestValidate(t *testing.T) {
	ok := []Metric{Reps(10), Seconds(30), Meters(400), LoadReps(5, 60)}
	for _, m := range ok {
		if err := m.Validate(); err != nil {
			t.Errorf("%s devia ser válida: %v", m.Type, err)
		}
	}
	bad := []Metric{
		{Type: MetricTime},
		{Type: MetricDistance},
		{Type: MetricLoadReps, Reps: intp(5)},
		{Type: "invented"},
	}
	for _, m := range bad {
		if err := m.Validate(); err == nil {
			t.Errorf("%+v devia falhar", m)
		}
	}
}

// Ida e volta a jsonb sem perda nos quatro tipos — o critério de aceitação de T2.2.
func TestRoundTripThroughJSONB(t *testing.T) {
	for _, want := range []Metric{Reps(12), Seconds(45), Meters(5000), LoadReps(8, 62.5)} {
		v, err := want.Value()
		if err != nil {
			t.Fatal(err)
		}
		var got Metric
		if err := got.Scan(v); err != nil {
			t.Fatal(err)
		}
		if got.Type != want.Type || got.Volume() != want.Volume() {
			t.Fatalf("%s: ida e volta perdeu informação: %+v ≠ %+v", want.Type, got, want)
		}
		// Os campos vazios não aparecem no JSON — uma métrica de repetições não
		// carrega `weightKg: null` para dentro da base de dados.
		var raw map[string]any
		if err := json.Unmarshal([]byte(v.(string)), &raw); err != nil {
			t.Fatal(err)
		}
		if _, has := raw["distanceMeters"]; has && want.Type != MetricDistance {
			t.Fatalf("%s: JSON leva campos que não são seus: %v", want.Type, raw)
		}
	}
}

func TestScanNil(t *testing.T) {
	var m Metric
	if err := m.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if m.Type != "" {
		t.Fatal("NULL devia dar métrica vazia")
	}
}

func intp(n int) *int { return &n }
