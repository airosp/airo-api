package nutrition

import "testing"

/*
 * Mostrar as opções em vez de um botão "trocar".
 *
 * ⚠️ Havia uma refeição por lugar e um botão que dava a seguinte. Quem não
 * gostasse tocava até acertar, sem saber quantas havia nem como voltar à que
 * tinha visto duas trocas atrás.
 */
func refeicaoDeEnsaio(t *testing.T) (Config, RebuildMealInput) {
	t.Helper()
	c := DefaultConfig()
	dieta := DietProfile{Style: "omnivore", MealsPerDay: 4, Budget: "medium"}
	refeicoes, err := BuildMeals(c, BuildMealsInput{
		DayISO: "2026-09-16", CalorieTarget: 2000,
		Macros: MacrosFloat{Protein: 150, Carbs: 200, Fat: 60},
		Diet:   dieta,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refeicoes) == 0 {
		t.Fatal("sem refeições para pedir opções")
	}
	return c, RebuildMealInput{Meal: refeicoes[1], Diet: dieta}
}

func TestAsOpcoesComecamPelaQueEstaNoPlano(t *testing.T) {
	c, in := refeicaoDeEnsaio(t)

	opcoes, err := OpcoesDeRefeicao(c, in, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(opcoes) == 0 {
		t.Fatal("nenhuma opção")
	}
	// A primeira é a que está no plano: as alternativas são alternativas **a
	// alguma coisa**, e tirar a actual fazia o ecrã propor uma troca a quem só
	// queria ver o que havia.
	if assinatura(opcoes[0].Items) != assinatura(in.Meal.Items) {
		t.Errorf("a primeira não é a do plano")
	}
}

// Cada opção é diferente das outras. Repetições para encher cartões são mentira.
func TestAsOpcoesNaoSeRepetem(t *testing.T) {
	c, in := refeicaoDeEnsaio(t)

	opcoes, err := OpcoesDeRefeicao(c, in, 3)
	if err != nil {
		t.Fatal(err)
	}
	vistas := map[string]bool{}
	for i, o := range opcoes {
		a := assinatura(o.Items)
		if vistas[a] {
			t.Errorf("opção %d repete %q", i, a)
		}
		vistas[a] = true
		// E cada uma continua a ser uma refeição a sério.
		if len(o.Items) == 0 {
			t.Errorf("opção %d veio vazia", i)
		}
		if o.Kcal != in.Meal.Kcal {
			t.Errorf("opção %d mudou o alvo: %d contra %d", i, o.Kcal, in.Meal.Kcal)
		}
	}
}

// Pedir zero devolve nada, e não rebenta.
func TestPedirZeroOpcoes(t *testing.T) {
	c, in := refeicaoDeEnsaio(t)
	opcoes, err := OpcoesDeRefeicao(c, in, 0)
	if err != nil || len(opcoes) != 0 {
		t.Fatalf("%v — %d", err, len(opcoes))
	}
}

/*
 * Pedir mais do que existe devolve o que existe.
 *
 * Uma dieta restrita com um alvo apertado tem mesmo poucas respostas, e é
 * melhor mostrar duas verdadeiras do que três com uma inventada.
 */
func TestPedirMaisDoQueExisteNaoInventa(t *testing.T) {
	c, in := refeicaoDeEnsaio(t)

	opcoes, err := OpcoesDeRefeicao(c, in, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(opcoes) > 50 {
		t.Fatalf("devolveu %d", len(opcoes))
	}
	vistas := map[string]bool{}
	for _, o := range opcoes {
		a := assinatura(o.Items)
		if vistas[a] {
			t.Fatalf("repetiu %q para encher", a)
		}
		vistas[a] = true
	}
}
