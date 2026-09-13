package nutrition

import "testing"

// O alvo acompanha o corpo e o treino — que é o que um alvo fixo não faz.
func TestAlvoDeAguaAcompanhaOCorpoEOTreino(t *testing.T) {
	c := DefaultConfig()

	leve := HydrationTarget(c, HydrationInput{BodyWeightKg: 55})
	pesado := HydrationTarget(c, HydrationInput{BodyWeightKg: 95})
	if leve >= pesado {
		t.Errorf("55 kg deu %d ml e 95 kg deu %d — quem pesa mais bebe mais", leve, pesado)
	}

	descanso := HydrationTarget(c, HydrationInput{BodyWeightKg: 75})
	treino := HydrationTarget(c, HydrationInput{BodyWeightKg: 75, TrainsToday: true, SessionMinutes: 60})
	if treino <= descanso {
		t.Errorf("com treino deu %d ml e sem treino %d — treinar dá sede", treino, descanso)
	}

	t.Logf("55 kg: %d ml · 95 kg: %d ml · 75 kg com 60 min: %d ml", leve, pesado, treino)
}

// Sem peso não se inventa um alvo preciso.
func TestSemPesoNaoSeInventaPrecisao(t *testing.T) {
	c := DefaultConfig()
	got := HydrationTarget(c, HydrationInput{BodyWeightKg: 0})
	if got != c.Hydration.FallbackMl {
		t.Errorf("sem peso deu %d, esperava o valor de recurso %d", got, c.Hydration.FallbackMl)
	}
}

// O piso e o tecto são absolutos.
//
// O tecto não é decorativo: beber demais dilui o sódio, e um alvo sem limite
// deixaria de ser um conselho.
func TestOsLimitesSaoAbsolutos(t *testing.T) {
	c := DefaultConfig()
	if got := HydrationTarget(c, HydrationInput{BodyWeightKg: 30}); got < c.Hydration.MinMl {
		t.Errorf("30 kg deu %d, abaixo do piso %d", got, c.Hydration.MinMl)
	}
	enorme := HydrationTarget(c, HydrationInput{
		BodyWeightKg: 200, TrainsToday: true, SessionMinutes: 240,
	})
	if enorme > c.Hydration.MaxMl {
		t.Errorf("deu %d, acima do tecto %d", enorme, c.Hydration.MaxMl)
	}
}

// Arredonda a 100 ml: ninguém mede 2 347 ml, e a precisão a mais faz uma
// estimativa parecer uma medida.
func TestArredondaACem(t *testing.T) {
	c := DefaultConfig()
	for _, peso := range []float64{52.3, 67.8, 81.1, 94.7} {
		got := HydrationTarget(c, HydrationInput{BodyWeightKg: peso})
		if got%100 != 0 {
			t.Errorf("%.1f kg deu %d ml — não é múltiplo de 100", peso, got)
		}
	}
}
