package training

import "testing"

/*
 * A taxonomia: um exercício pode atravessar mais do que um padrão, e a pancada
 * no chão não é a mesma coisa que a dificuldade.
 */

func TestUmBurpeeNaoECardioESoIsso(t *testing.T) {
	lib, err := Library()
	if err != nil {
		t.Fatal(err)
	}
	var burpee Exercise
	for _, e := range lib {
		if e.ID == "burpee" {
			burpee = e
		}
	}
	if burpee.ID == "" {
		t.Fatal("sem burpee na biblioteca")
	}

	ps := PadroesDe(burpee)
	if len(ps) < 3 {
		t.Fatalf("o burpee ficou com %v — é agachamento, empurrar e cardio", ps)
	}
	if ImpactoDe(burpee) != ImpactoAlto {
		t.Errorf("impacto do burpee: %q", ImpactoDe(burpee))
	}
}

// Sem `patterns` declarado, os padrões são o principal — e nada mais.
func TestSemListaOsPadroesSaoOPrincipal(t *testing.T) {
	e := Exercise{ID: "pushup", Pattern: Push}
	ps := PadroesDe(e)
	if len(ps) != 1 || ps[0] != Push {
		t.Fatalf("veio %v", ps)
	}
	if ImpactoDe(e) != ImpactoBaixo {
		t.Errorf("sem impacto declarado devia ser baixo, veio %q", ImpactoDe(e))
	}
}

func TestOTectoDeImpacto(t *testing.T) {
	alto := Exercise{ID: "burpee", Impact: ImpactoAlto}
	baixo := Exercise{ID: "plank"}

	// Sem tecto cabe tudo.
	if !CabeNoImpacto(alto, "") || !CabeNoImpacto(baixo, "") {
		t.Error("sem tecto devia caber tudo")
	}
	if CabeNoImpacto(alto, ImpactoBaixo) {
		t.Error("um burpee não cabe num tecto baixo")
	}
	if !CabeNoImpacto(baixo, ImpactoBaixo) {
		t.Error("uma prancha cabe em qualquer tecto")
	}
	if !CabeNoImpacto(alto, ImpactoAlto) {
		t.Error("um tecto alto aceita tudo")
	}
}

/*
 * Com tecto baixo, os saltos saem da escolha — e o treino continua a ser um
 * treino.
 *
 * Tirar exercícios não pode deixar buracos: quem escolhe não saltar não está a
 * pedir menos treino.
 */
func TestTectoBaixoTiraSaltosENaoDeixaBuraco(t *testing.T) {
	c := DefaultConfig()
	base := BuildInput{
		PlanLabel: "Full Body", Experience: Intermediate,
		Equipment: []string{}, Minutes: 40, DayISO: "2026-09-16",
	}

	livre, err := BuildSession(c, base)
	if err != nil {
		t.Fatal(err)
	}
	comTecto := base
	comTecto.MaxImpact = ImpactoBaixo
	travada, err := BuildSession(c, comTecto)
	if err != nil {
		t.Fatal(err)
	}

	if len(travada.Exercises) == 0 {
		t.Fatal("o treino ficou vazio")
	}
	// O orçamento é o mesmo, logo o treino tem de continuar do mesmo tamanho.
	if len(travada.Exercises) < len(livre.Exercises)-1 {
		t.Errorf("de %d exercícios para %d: abriu buraco",
			len(livre.Exercises), len(travada.Exercises))
	}

	lib, _ := Library()
	porID := map[string]Exercise{}
	for _, e := range lib {
		porID[e.ID] = e
	}
	for _, se := range travada.Exercises {
		if ImpactoDe(porID[se.Exercise.ID]) != ImpactoBaixo {
			t.Errorf("%q tem impacto %q e entrou num tecto baixo",
				se.Exercise.ID, ImpactoDe(porID[se.Exercise.ID]))
		}
	}
}

/*
 * Os objectivos saem de uma regra, não de sessenta campos escritos à mão.
 *
 * O vocabulário é o mesmo que a pessoa escolhe no plano, para a comparação ser
 * directa.
 */
func TestObjectivosDeUmExercicio(t *testing.T) {
	casos := []struct {
		e    Exercise
		quer string
	}{
		{Exercise{Pattern: Squat, Equipment: []string{"barbell"}, Level: Intermediate}, "strength"},
		{Exercise{Pattern: Push, Level: Beginner}, "muscle"},
		{Exercise{Pattern: Cardio, Level: Beginner}, "fatLoss"},
		{Exercise{Pattern: Mobility, Level: Beginner}, "habit"},
	}
	for _, c := range casos {
		if !contem(ObjectivosDe(c.e), c.quer) {
			t.Errorf("%v: %v não tem %q", c.e.Pattern, ObjectivosDe(c.e), c.quer)
		}
	}

	// Peso do corpo de iniciante não constrói força: constrói o hábito e algum
	// músculo. Dizer que sim era prometer o que não acontece.
	if contem(ObjectivosDe(Exercise{Pattern: Push, Level: Beginner}), "strength") {
		t.Error("flexões de iniciante não são treino de força")
	}
	// Tudo serve para criar o hábito.
	for _, p := range []MovementPattern{Push, Pull, Squat, Cardio, Core, Mobility} {
		if !contem(ObjectivosDe(Exercise{Pattern: p}), "habit") {
			t.Errorf("%q não serve o hábito", p)
		}
	}
}

func contem(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
