package portable

import (
	"encoding/json"
	"os"
	"testing"
)

// Compara SeedFrom com os valores produzidos pela implementação TypeScript
// real, gerados por node a partir de build-session.ts.
func TestSeedFromAgainstNode(t *testing.T) {
	// O ficheiro é gerado por node a correr a função de build-session.ts e
	// fica versionado: assim a comparação corre em CI, e não só na máquina de
	// quem a fez.
	raw, err := os.ReadFile("testdata/seeds-from-typescript.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases [][]any
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		in := c[0].(string)
		want := int(c[1].(float64))
		if got := SeedFrom(in); got != want {
			t.Errorf("SeedFrom(%q) = %d, node dá %d", in, got, want)
		}
	}
	t.Logf("%d casos comparados com a implementação TypeScript", len(cases))
}
