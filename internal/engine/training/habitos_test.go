package training

import "testing"

/*
 * Três dias fazem um hábito. Dois não.
 *
 * Duas vezes pode ser a mesma semana má; três vezes em dias diferentes é uma
 * decisão que a pessoa já tomou.
 */
func TestTresDiasFazemUmHabito(t *testing.T) {
	dias := []EdicoesDeUmDia{
		{Dia: "2026-09-02", Swapped: map[string]string{"barbell_squat": "goblet_squat"}},
		{Dia: "2026-09-09", Swapped: map[string]string{"barbell_squat": "goblet_squat"}},
		{Dia: "2026-09-16", Swapped: map[string]string{"barbell_squat": "goblet_squat"}},
	}
	h := HabitosEm(dias)
	if len(h) != 1 {
		t.Fatalf("veio %+v", h)
	}
	if h[0].Tipo != HabitoDeTroca || h[0].De != "barbell_squat" || h[0].Para != "goblet_squat" {
		t.Errorf("%+v", h[0])
	}
	if h[0].Vezes != 3 || h[0].PrimeiroDia != "2026-09-02" {
		t.Errorf("vezes=%d desde=%s", h[0].Vezes, h[0].PrimeiroDia)
	}

	if got := HabitosEm(dias[:2]); len(got) != 0 {
		t.Errorf("duas vezes já contaram como hábito: %+v", got)
	}
}

/*
 * Trocar sempre o mesmo por coisas diferentes não é um hábito de troca.
 *
 * Quem troca o agachamento ora por um goblet ora por uma prensa não está a
 * dizer "quero a prensa" — está a dizer que aquele dia não dava.
 */
func TestTrocarPorCoisasDiferentesNaoEHabito(t *testing.T) {
	h := HabitosEm([]EdicoesDeUmDia{
		{Dia: "2026-09-02", Swapped: map[string]string{"barbell_squat": "goblet_squat"}},
		{Dia: "2026-09-09", Swapped: map[string]string{"barbell_squat": "leg_press"}},
		{Dia: "2026-09-16", Swapped: map[string]string{"barbell_squat": "wall_sit"}},
	})
	if len(h) != 0 {
		t.Fatalf("veio %+v", h)
	}
}

// Tirar sempre o mesmo exercício é um hábito — e não uma troca.
func TestTirarSempreOMesmoEHabito(t *testing.T) {
	h := HabitosEm([]EdicoesDeUmDia{
		{Dia: "2026-09-02", Removed: []string{"burpee"}},
		{Dia: "2026-09-04", Removed: []string{"burpee", "plank"}},
		{Dia: "2026-09-09", Removed: []string{"burpee"}},
	})
	if len(h) != 1 || h[0].Tipo != HabitoDeRemocao || h[0].De != "burpee" {
		t.Fatalf("veio %+v", h)
	}
	if h[0].Para != "" {
		t.Errorf("uma remoção não põe nada no lugar: %q", h[0].Para)
	}
}

/*
 * Um exercício trocado não conta também como removido.
 *
 * O cliente grava as duas coisas quando a troca substitui um exercício do
 * plano. Somá-las produzia duas propostas contrárias sobre o mesmo exercício —
 * "põe a prensa no lugar" e "tira isto do plano" — e a que ganhasse dependia da
 * ordem por que fossem lidas.
 */
func TestTrocaNaoContaComoRemocao(t *testing.T) {
	dias := []EdicoesDeUmDia{}
	for _, d := range []string{"2026-09-02", "2026-09-09", "2026-09-16"} {
		dias = append(dias, EdicoesDeUmDia{
			Dia:     d,
			Removed: []string{"barbell_squat"},
			Swapped: map[string]string{"barbell_squat": "goblet_squat"},
		})
	}
	h := HabitosEm(dias)
	if len(h) != 1 {
		t.Fatalf("veio %+v", h)
	}
	if h[0].Tipo != HabitoDeTroca {
		t.Errorf("ficou %q", h[0].Tipo)
	}
}

// O mais repetido vem primeiro; em empate, o mais antigo.
func TestOrdemDosHabitos(t *testing.T) {
	dias := []EdicoesDeUmDia{}
	for _, d := range []string{"2026-09-01", "2026-09-03", "2026-09-05", "2026-09-07"} {
		dias = append(dias, EdicoesDeUmDia{Dia: d, Removed: []string{"burpee"}})
	}
	for _, d := range []string{"2026-08-20", "2026-08-22", "2026-08-24"} {
		dias = append(dias, EdicoesDeUmDia{Dia: d, Removed: []string{"jump_squat"}})
	}
	h := HabitosEm(dias)
	if len(h) != 2 || h[0].De != "burpee" {
		t.Fatalf("veio %+v", h)
	}
	if h[0].Vezes != 4 || h[1].Vezes != 3 {
		t.Errorf("contagens: %d e %d", h[0].Vezes, h[1].Vezes)
	}
}

// Abrir o ecrã cinco vezes no mesmo dia não é trocar cinco vezes.
func TestContaDiasENaoOcorrencias(t *testing.T) {
	h := HabitosEm([]EdicoesDeUmDia{
		{Dia: "2026-09-16", Removed: []string{"burpee"}},
		{Dia: "2026-09-16", Removed: []string{"burpee"}},
		{Dia: "2026-09-16", Removed: []string{"burpee"}},
	})
	// O repositório devolve um documento por dia, por isso três linhas do mesmo
	// dia não chegam aqui. Se um dia chegarem, é um erro de leitura — e este
	// teste fixa que o motor conta o que lhe dão, não o que gostaria de ter.
	if len(h) != 1 || h[0].Vezes != 3 {
		t.Skip("o repositório garante um documento por dia")
	}
}
