package portable

import (
	"encoding/json"
	"os"
	"testing"
)

// RoundTo tem de dar exactamente o que `Number(x.toFixed(n))` dá.
//
// Os valores são gerados por node, não escritos à mão — e incluem de propósito
// os casos onde a implementação ingénua (`x * 10^n`) diverge.
func TestRoundToMatchesToFixed(t *testing.T) {
	raw, err := os.ReadFile("testdata/tofixed-from-typescript.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases [][]float64
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		in, want2, want3 := c[0], c[1], c[2]
		if got := RoundTo(in, 2); got != want2 {
			t.Errorf("RoundTo(%v, 2) = %v, o toFixed dá %v", in, got, want2)
		}
		if got := RoundTo(in, 3); got != want3 {
			t.Errorf("RoundTo(%v, 3) = %v, o toFixed dá %v", in, got, want3)
		}
	}
	t.Logf("%d valores iguais ao toFixed do JavaScript", len(cases))
}

// O caso concreto que a paridade da adesão apanhou.
func TestRoundToRegressionFromAdherence(t *testing.T) {
	if got := RoundTo(114.0/240.0, 2); got != 0.47 {
		t.Fatalf("114/240 a duas casas = %v, o cliente mostra 0,47", got)
	}
}
