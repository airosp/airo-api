package training

import "math"

/*
 * A progressão de carga.
 *
 * ⚠️ **Propõe; não manda.** O número aparece no comando já preenchido e quem
 * treina muda-o com um toque. A app não sabe se a pessoa dormiu mal, se comeu,
 * se a barra do ginásio é a mesma — sabe o que ficou registado, e é só isso que
 * usa.
 *
 * A regra é a mais simples que funciona, e é a que os métodos de progressão
 * linear usam há décadas: se a última vez saiu inteira, sobe um degrau; se
 * ficou a meio, repete o mesmo peso. Nunca desce sozinha — baixar é uma decisão
 * de quem treina, e uma app que baixa o peso a quem faltou uma semana é uma app
 * que castiga.
 */

// O degrau: dois quilos e meio, que é o disco mais pequeno de um ginásio.
const DegrauDeCarga = 2.5

// Acima disto, o degrau passa a ser proporcional: subir 2,5 kg num agachamento
// de 140 é menos de 2%, e subir 2,5 num bíceps de 10 é um quarto.
const cargaOndeODegrauMuda = 60.0

type PropostaDeCarga struct {
	ExerciseID string
	// UltimaKg é o que se levantou da última vez. Zero quer dizer "nunca".
	UltimaKg float64
	// SugestaoKg é o que a Airo propõe para hoje.
	SugestaoKg float64
	// Motivo, por palavras, para o ecrã não ter de o deduzir.
	Motivo string
}

// PropoeCarga aplica a regra a uma última sessão.
func PropoeCarga(exercicio string, ultimaKg float64, completou bool) PropostaDeCarga {
	p := PropostaDeCarga{ExerciseID: exercicio, UltimaKg: ultimaKg}

	if ultimaKg <= 0 {
		// Sem história não se inventa um número: um peso proposto do nada é um
		// palpite com ar de recomendação.
		p.Motivo = "Primeira vez — escreve o peso que usares."
		return p
	}
	if !completou {
		p.SugestaoKg = ultimaKg
		p.Motivo = "Da última vez ficou a meio. Repete o mesmo peso."
		return p
	}

	degrau := DegrauDeCarga
	if ultimaKg >= cargaOndeODegrauMuda {
		// 4% arredondado ao meio quilo: o degrau acompanha a carga em vez de
		// ficar simbólico.
		degrau = math.Round(ultimaKg*0.04*2) / 2
		if degrau < DegrauDeCarga {
			degrau = DegrauDeCarga
		}
	}
	p.SugestaoKg = ultimaKg + degrau
	p.Motivo = "Última vez saiu inteira. Sobe um degrau."
	return p
}
