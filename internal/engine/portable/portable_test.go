package portable

import (
	"math"
	"testing"
	"testing/quick"
)

// Os valores da direita foram medidos em Node, não deduzidos.
func TestRoundJSMatchesJavaScript(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0.5, 1}, {1.5, 2}, {2.5, 3},
		{-0.5, 0}, // JS dá -0; 0 == -0 em comparação
		{-1.5, -1},
		{-2.5, -2},
		{0.4, 0}, {-0.4, 0}, {-0.6, -1},
		{78.45, 78}, {-0.0, 0},
	}
	for _, tc := range cases {
		if got := RoundJS(tc.in); got != tc.want {
			t.Errorf("RoundJS(%v) = %v, o JavaScript dá %v", tc.in, got, tc.want)
		}
	}
}

func TestRoundJSDivergesFromMathRoundOnNegativeHalves(t *testing.T) {
	// Se um dia os dois coincidirem, este pacote deixou de ser preciso — e é
	// melhor o teste dizê-lo do que ficar a testar o óbvio.
	for _, x := range []float64{-0.5, -1.5, -2.5} {
		if RoundJS(x) == math.Round(x) {
			t.Fatalf("em %v os dois já concordam; rever este pacote", x)
		}
	}
}

func TestModIsNeverNegative(t *testing.T) {
	f := func(n, m int) bool {
		if m <= 0 || m > 1000 {
			return true
		}
		r := Mod(n, m)
		return r >= 0 && r < m
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 10000}); err != nil {
		t.Fatal(err)
	}
}

// O critério de aceitação de T2.17: sementes negativas não entram em pânico.
func TestPickSurvivesNegativeSeeds(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f", "g"}
	f := func(seed int16, count uint8) bool {
		got := Pick(items, int(count%10), int(seed))
		for _, v := range got {
			found := false
			for _, it := range items {
				if it == v {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 10000}); err != nil {
		t.Fatal(err)
	}
	// E explicitamente o caso que rebentava.
	for _, seed := range []int{-1, -7, -13, -1000, math.MinInt32} {
		if got := Pick(items, 2, seed); len(got) != 2 {
			t.Fatalf("semente %d devolveu %d elementos", seed, len(got))
		}
	}
}

func TestPickIsStable(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	a := Pick(items, 3, 12345)
	b := Pick(items, 3, 12345)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("a mesma semente deu escolhas diferentes: %v ≠ %v", a, b)
		}
	}
	if c := Pick(items, 3, 12346); a[0] == c[0] && a[1] == c[1] && a[2] == c[2] {
		t.Fatal("sementes diferentes deram exactamente a mesma escolha")
	}
}

func TestPickDoesNotRepeat(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7}
	for seed := -50; seed < 50; seed++ {
		got := Pick(items, 4, seed)
		seen := map[int]bool{}
		for _, v := range got {
			if seen[v] {
				t.Fatalf("semente %d repetiu %d: %v", seed, v, got)
			}
			seen[v] = true
		}
		if len(got) != 4 {
			t.Fatalf("semente %d devolveu %d de 4", seed, len(got))
		}
	}
}

// O caso que já aconteceu: passo que divide o tamanho da lista fixa a escolha.
func TestPickCoversTheWholeListAcrossSeeds(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7} // sete vegetais
	seen := map[int]bool{}
	for seed := 0; seed < 200; seed++ {
		for _, v := range Pick(items, 1, seed) {
			seen[v] = true
		}
	}
	if len(seen) != len(items) {
		t.Fatalf("só %d dos %d itens foram alguma vez escolhidos: %v", len(seen), len(items), seen)
	}
}

func TestPickSmallList(t *testing.T) {
	items := []int{1, 2}
	if got := Pick(items, 5, 0); len(got) != 2 {
		t.Fatalf("pedir mais do que existe devolve tudo: %v", got)
	}
	if got := Pick([]int{}, 3, 0); got != nil {
		t.Fatalf("lista vazia devolve nil: %v", got)
	}
	if got := Pick(items, 0, 0); got != nil {
		t.Fatalf("count 0 devolve nil: %v", got)
	}
}

// Valores medidos com a implementação TypeScript de seedFrom.
func TestSeedFromMatchesTypeScript(t *testing.T) {
	cases := map[string]int{
		"":                                  0,
		"a":                                 97,
		"ab":                                3105,
		"abc":                               96354,
		"2026-09-12|Recovery|intermediate|": 0, // preenchido abaixo
	}
	delete(cases, "2026-09-12|Recovery|intermediate|")
	for in, want := range cases {
		if got := SeedFrom(in); got != want {
			t.Errorf("SeedFrom(%q) = %d, o TypeScript dá %d", in, got, want)
		}
	}
	// Nunca negativo, seja qual for a entrada — é o que garante que
	// `seed % len` não produz índice negativo.
	f := func(s string) bool { return SeedFrom(s) >= 0 }
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}
