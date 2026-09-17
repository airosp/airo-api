package training

import "testing"

// Sem história, não se propõe número nenhum — um peso vindo do nada é um
// palpite com ar de recomendação.
func TestSemHistoriaNaoSePropoeNada(t *testing.T) {
	p := PropoeCarga("back_squat", 0, false)
	if p.SugestaoKg != 0 {
		t.Errorf("propôs %v sem nunca ter visto a pessoa levantar nada", p.SugestaoKg)
	}
	if p.Motivo == "" {
		t.Error("sem motivo: o ecrã fica com um campo vazio e sem explicação")
	}
}

// Sessão inteira, sobe um degrau.
func TestSessaoInteiraSobeUmDegrau(t *testing.T) {
	p := PropoeCarga("db_bench", 20, true)
	if p.SugestaoKg != 22.5 {
		t.Errorf("propôs %v, esperava 22.5", p.SugestaoKg)
	}
}

// Sessão a meio, repete — não desce.
func TestSessaoAMeioRepeteEmVezDeDescer(t *testing.T) {
	p := PropoeCarga("db_bench", 20, false)
	if p.SugestaoKg != 20 {
		t.Errorf("propôs %v, esperava repetir 20", p.SugestaoKg)
	}
}

/*
 * O degrau acompanha a carga.
 *
 * 2,5 kg num agachamento de 140 é menos de 2%; num bíceps de 10 é um quarto.
 * Acima de 60 kg o degrau passa a 4%, arredondado ao meio quilo.
 */
func TestODegrauAcompanhaACarga(t *testing.T) {
	leve := PropoeCarga("curl", 10, true)
	if leve.SugestaoKg != 12.5 {
		t.Errorf("leve: propôs %v, esperava 12.5", leve.SugestaoKg)
	}

	pesado := PropoeCarga("back_squat", 140, true)
	// 140 × 4% = 5,6 → 5,5 ao meio quilo.
	if pesado.SugestaoKg != 145.5 {
		t.Errorf("pesado: propôs %v, esperava 145.5", pesado.SugestaoKg)
	}
	if pesado.SugestaoKg-140 < DegrauDeCarga {
		t.Error("o degrau ficou menor do que o disco mais pequeno")
	}
}

// Nunca desce sozinha: baixar é decisão de quem treina.
func TestNuncaDesceSozinha(t *testing.T) {
	for _, completou := range []bool{true, false} {
		if p := PropoeCarga("x", 50, completou); p.SugestaoKg < 50 {
			t.Errorf("completou=%v: propôs %v, abaixo dos 50 que já levantou", completou, p.SugestaoKg)
		}
	}
}
