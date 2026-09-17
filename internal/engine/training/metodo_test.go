package training

import "testing"

/*
 * O que um especialista faz ao teu treino.
 *
 * ⚠️ Escolher a Ana ou o Miguel era escolher um nome por baixo do título das
 * aulas: o plano saía exactamente igual. Uma escolha sem consequência é uma
 * pergunta a fingir.
 */

func sessaoCom(t *testing.T, especialista string) Session {
	t.Helper()
	s, err := BuildSession(DefaultConfig(), BuildInput{
		PlanLabel: "Full Body", Experience: Intermediate,
		Equipment: []string{"dumbbells"}, Minutes: 45, DayISO: "2026-09-17",
		Specialist: especialista,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func principais(s Session) []SessionExercise {
	var out []SessionExercise
	for _, e := range s.Exercises {
		if e.Role == Main {
			out = append(out, e)
		}
	}
	return out
}

/*
 * A Ana dá mais séries por exercício — e menos exercícios, porque o tempo é o
 * mesmo. A troca é essa, e é explicável numa frase.
 */
func TestATreinadoraDeForcaDaMaisSeriesPorExercicio(t *testing.T) {
	sem := principais(sessaoCom(t, ""))
	com := principais(sessaoCom(t, "ana-silva"))

	if len(sem) == 0 || len(com) == 0 {
		t.Fatal("sessão vazia")
	}
	mediaSeries := func(es []SessionExercise) float64 {
		var total int
		for _, e := range es {
			total += e.Sets
		}
		return float64(total) / float64(len(es))
	}
	if mediaSeries(com) <= mediaSeries(sem) {
		t.Errorf("com a Ana: %.1f séries por exercício; sem ninguém: %.1f",
			mediaSeries(com), mediaSeries(sem))
	}
	if FraseDoMetodo("ana-silva") == "" {
		t.Error("sem frase, o método é magia")
	}
}

/*
 * O fisioterapeuta tira o impacto. É o que ele faz na vida real, e é o que faz
 * aqui.
 */
func TestOFisioterapeutaTiraOImpacto(t *testing.T) {
	s := sessaoCom(t, "diogo-ramos")

	lib, _ := Library()
	porID := map[string]Exercise{}
	for _, e := range lib {
		porID[e.ID] = e
	}
	for _, se := range s.Exercises {
		if ImpactoDe(porID[se.Exercise.ID]) != ImpactoBaixo {
			t.Errorf("%q tem impacto %q e entrou num treino do fisioterapeuta",
				se.Exercise.ID, ImpactoDe(porID[se.Exercise.ID]))
		}
	}
	if len(s.Exercises) == 0 {
		t.Error("o treino ficou vazio — tirou-se de mais")
	}
}

/*
 * O método **aperta** o tecto da pessoa, nunca o afrouxa.
 *
 * Quem escolheu não saltar não passa a saltar por ter posto um treinador na
 * equipa.
 */
func TestOMetodoNaoAfrouxaOTectoDaPessoa(t *testing.T) {
	c := DefaultConfig()
	s, err := BuildSession(c, BuildInput{
		PlanLabel: "Full Body", Experience: Intermediate,
		Equipment: []string{}, Minutes: 40, DayISO: "2026-09-17",
		MaxImpact: ImpactoBaixo,
		// Um treinador cujo método não mexe no impacto.
		Specialist: "ana-silva",
	})
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := Library()
	porID := map[string]Exercise{}
	for _, e := range lib {
		porID[e.ID] = e
	}
	for _, se := range s.Exercises {
		if ImpactoDe(porID[se.Exercise.ID]) != ImpactoBaixo {
			t.Errorf("%q passou o tecto que a pessoa pôs", se.Exercise.ID)
		}
	}
}

// Um especialista sem método não muda nada — e não rebenta.
func TestUmEspecialistaSemMetodoNaoMudaNada(t *testing.T) {
	sem := sessaoCom(t, "")
	comNutricionista := sessaoCom(t, "carla-mendes")

	if len(sem.Exercises) != len(comNutricionista.Exercises) {
		t.Errorf("a nutricionista mexeu no treino: %d contra %d exercícios",
			len(comNutricionista.Exercises), len(sem.Exercises))
	}
	if FraseDoMetodo("carla-mendes") != "" {
		t.Error("diz ter método e não tem")
	}
	if FraseDoMetodo("um-que-nao-existe") != "" {
		t.Error("inventou um método")
	}
}

// E o treino continua a caber no tempo que a pessoa reservou.
func TestOMetodoNaoEstoiraOOrcamento(t *testing.T) {
	c := DefaultConfig()
	for _, especialista := range []string{"", "ana-silva", "nuno-batista", "rita-campos"} {
		s, err := BuildSession(c, BuildInput{
			PlanLabel: "Full Body", Experience: Intermediate,
			Equipment: []string{"dumbbells"}, Minutes: 45, DayISO: "2026-09-17",
			Specialist: especialista,
		})
		if err != nil {
			t.Fatal(err)
		}
		minutos := float64(s.EstimatedSeconds) / 60
		if minutos > 45*1.15 {
			t.Errorf("%q: %.0f minutos para um orçamento de 45", especialista, minutos)
		}
		if len(s.Exercises) == 0 {
			t.Errorf("%q: treino vazio", especialista)
		}
	}
}
