package view

import (
	"testing"

	"github.com/airosp/airo-api/internal/engine/journey"
)

/*
 * A escala do gráfico decide-se aqui, e não no telemóvel.
 *
 * ⚠️ O telemóvel escalava ao mínimo e ao máximo da série. Isso mente das duas
 * maneiras: dois quilos perdidos em três meses ficam com o aspecto de um
 * precipício, e duzentos gramas de ruído num dia mau também. Pior — o mesmo
 * peso lia-se diferente em dois aparelhos com históricos diferentes.
 */

func TestAEscalaContemAPartidaEOAlvo(t *testing.T) {
	e := escalaDo(SnapshotData{Baseline: 82, Target: 75, Unidade: "kg"})
	if e == nil {
		t.Fatal("sem escala")
	}
	if e.Min > 75 || e.Max < 82 {
		t.Errorf("a escala %v–%v corta o percurso de 82 a 75", e.Min, e.Max)
	}
	if e.Target == nil || *e.Target != 75 {
		t.Errorf("sem a linha do alvo: %+v", e.Target)
	}
	if e.Unit != "kg" {
		t.Errorf("unidade %q", e.Unit)
	}
}

/*
 * Quem passou do alvo continua a ver-se no gráfico.
 *
 * Perder mais do que o previsto não pode pôr a própria linha fora do quadro.
 */
func TestQuemPassouDoAlvoContinuaNoGrafico(t *testing.T) {
	e := escalaDo(SnapshotData{
		Baseline: 82, Target: 75, Unidade: "kg",
		Trend: &journey.Trend{RollingAverage: 72.4},
	})
	if e == nil || e.Min > 72.4 {
		t.Fatalf("a escala %v–%v deixa de fora o peso de agora (72,4)", e.Min, e.Max)
	}
}

/*
 * Um alvo perto da partida não dá um gráfico esmagado.
 *
 * Quem quer perder um quilo tem direito a um eixo que se leia, e não a uma
 * linha colada às bordas.
 */
func TestUmAlvoPertoDaPartidaTemAr(t *testing.T) {
	e := escalaDo(SnapshotData{Baseline: 70, Target: 69.5, Unidade: "kg"})
	if e == nil {
		t.Fatal("sem escala")
	}
	if e.Max-e.Min < 1.5 {
		t.Errorf("intervalo de %v — esmagado", e.Max-e.Min)
	}
}

// Sem alvo não há escala: um objectivo de hábito não tem peso a atingir.
func TestSemAlvoNaoHaEscala(t *testing.T) {
	if e := escalaDo(SnapshotData{Baseline: 70}); e != nil {
		t.Fatalf("inventou uma escala: %+v", e)
	}
	if e := escalaDo(SnapshotData{Target: 70}); e != nil {
		t.Fatalf("inventou uma escala sem partida: %+v", e)
	}
}

/*
 * Os limites arredondam ao meio quilo.
 *
 * Um eixo que diz 73,4 a 78,9 parece um erro; 73,5 a 79 parece uma escala.
 */
func TestOsLimitesSaoRedondos(t *testing.T) {
	e := escalaDo(SnapshotData{Baseline: 81.3, Target: 74.7, Unidade: "kg"})
	if e == nil {
		t.Fatal("sem escala")
	}
	for _, v := range []float64{e.Min, e.Max} {
		if v*2 != float64(int(v*2)) {
			t.Errorf("%v não é meio quilo redondo", v)
		}
	}
}

// Ganhar peso é um percurso como outro qualquer: a escala não assume direcção.
func TestGanharPesoTambemTemEscala(t *testing.T) {
	e := escalaDo(SnapshotData{Baseline: 58, Target: 65, Unidade: "kg"})
	if e == nil || e.Min > 58 || e.Max < 65 {
		t.Fatalf("%+v", e)
	}
}
