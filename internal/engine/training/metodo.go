package training

/*
 * O que um especialista faz ao teu treino.
 *
 * ⚠️ Escolher a Ana ou o Miguel era escolher um nome por baixo do título das
 * aulas. O plano saía exactamente igual, e a pergunta que a app fazia — "quem
 * queres na tua equipa?" — não tinha consequência nenhuma. Uma escolha sem
 * consequência é uma pergunta a fingir.
 *
 * Um método é **um viés sobre o que o motor já faz**, e não um plano paralelo.
 * Três razões:
 *
 *  1. O que o motor monta está provado passo a passo contra o cliente. Um
 *     segundo caminho seria uma segunda coisa para manter em dia.
 *  2. Os limites de segurança continuam a ser os mesmos. Um método não pode
 *     propor a alguém que está a começar o que se propõe a quem treina há anos.
 *  3. A mudança tem de ser **explicável numa frase**. Se não se consegue dizer
 *     à pessoa o que mudou por ter escolhido a Rita, não mudou nada que ela
 *     queira.
 */

// Metodo é o viés de um especialista sobre o treino.
type Metodo struct {
	// ID do especialista a que pertence.
	ID string
	/*
	 * SetsExtra acrescenta séries aos exercícios principais. Negativo tira.
	 *
	 * Limitado a ±1 de propósito: duas séries a mais num treino de quarenta
	 * minutos não cabem, e o acerto ao orçamento tirava-as a seguir sem
	 * ninguém perceber porquê.
	 */
	SetsExtra int
	/*
	 * PadraoPreferido entra à frente na escolha, quando existe e cabe.
	 *
	 * Não **substitui** o foco do dia: um dia de pernas com um método de
	 * mobilidade continua a ser um dia de pernas, com mais mobilidade dentro.
	 */
	PadraoPreferido MovementPattern
	/** TectoDeImpacto aperta o impacto máximo, quando o método o pede. */
	TectoDeImpacto Impact
	/** Frase é o que se diz à pessoa. Sem isto, o método é magia. */
	Frase string
}

/*
 * metodos: o que cada treinador faz de diferente.
 *
 * Só treinadores. Um nutricionista não mexe no treino, e um fisioterapeuta
 * mexe — mas o que ele faz é baixar o impacto, que é o que está aqui. O
 * treinador manda no plano; os outros acrescentam.
 */
var metodos = map[string]Metodo{
	"ana-silva": {
		ID: "ana-silva", SetsExtra: 1,
		Frase: "A Ana acrescenta uma série aos exercícios principais — é onde a força se constrói.",
	},
	"miguel-torres": {
		ID: "miguel-torres", PadraoPreferido: Cardio,
		Frase: "O Miguel puxa o cardio para dentro do treino, em vez de o deixar para o fim.",
	},
	"rita-campos": {
		ID: "rita-campos", PadraoPreferido: Mobility, TectoDeImpacto: ImpactoModerado,
		Frase: "A Rita põe mais mobilidade e tira os saltos — o corpo primeiro, a carga depois.",
	},
	"nuno-batista": {
		ID: "nuno-batista", PadraoPreferido: Cardio, SetsExtra: -1,
		Frase: "O Nuno troca volume por resistência: menos séries, mais tempo em movimento.",
	},
	"diogo-ramos": {
		ID: "diogo-ramos", TectoDeImpacto: ImpactoBaixo,
		Frase: "O Diogo tira o impacto do treino enquanto as articulações precisarem.",
	},
}

/*
 * MetodoDe devolve o método de um especialista, se ele tiver um.
 *
 * Um identificador desconhecido não é erro: pode ser um especialista novo que
 * ainda não tem método, ou um que saiu. Nos dois casos o treino sai como saía.
 */
func MetodoDe(id string) (Metodo, bool) {
	m, ok := metodos[id]
	return m, ok
}

/*
 * FraseDoMetodo é o que se diz à pessoa sobre o que mudou.
 *
 * ⚠️ Sem isto o método é magia: o treino sai diferente e ninguém sabe porquê.
 * Uma app que muda o plano sem dizer porquê ensina a não confiar nela.
 *
 * Vazio quando o especialista não tem método — e aí não há nada a dizer.
 */
func FraseDoMetodo(id string) string {
	if m, ok := metodos[id]; ok {
		return m.Frase
	}
	return ""
}
