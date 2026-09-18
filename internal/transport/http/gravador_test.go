package http_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

/*
 * O contrato, gravado pelos testes que já existem.
 *
 * ⚠️ O problema: os tipos do cliente são escritos à mão ao lado dos do
 * servidor, em duas línguas, e nada os confronta. Custou um `422` — o cliente
 * mandava `targetKg` onde o servidor lê `targetWeightKg`.
 *
 * E os golden files **não apanhavam esse**: gravam respostas, e aquele era um
 * campo de pedido. Escrever quarenta e três testes de resposta à mão teria
 * custado um dia e deixado a porta aberta na direcção por onde o erro entrou.
 *
 * Isto grava as duas direcções, e não custa um teste novo: cada pedido que a
 * suite já faz passa por cinco funções — `get`, `post`, `put`, `patch`, `del`
 * — e é aí que se grava. Quem acrescentar um teste acrescenta cobertura de
 * contrato sem dar por isso.
 *
 * **Só o que o servidor aceitou.** Um pedido que devolveu 422 diz o que ele
 * recusa, não o que ele aceita, e pôr esses campos no contrato seria ensinar o
 * cliente a mandar o que não serve.
 *
 *	AIRO_GRAVAR_CONTRATO=1 go test ./internal/transport/http/
 *
 * As rotas que nenhum teste toca ficam **de fora**, e o gerador diz quantas
 * são. É a diferença entre uma cobertura verdadeira e um número a fingir.
 */

type chamada struct {
	Metodo   string            `json:"metodo"`
	Caminho  string            `json:"caminho"`
	Status   int               `json:"status"`
	Pedido   map[string]string `json:"pedido,omitempty"`
	Resposta map[string]string `json:"resposta,omitempty"`
}

var (
	gravadas   = map[string]chamada{}
	gravadasMu sync.Mutex
)

func aGravar() bool { return os.Getenv("AIRO_GRAVAR_CONTRATO") == "1" }

/*
 * gravar guarda a forma de um pedido e da sua resposta.
 *
 * Chamado de dentro dos ajudantes, e por isso invisível para quem escreve
 * testes. Quando a mesma rota é chamada várias vezes, fica a chamada com **mais
 * campos**: é a que descreve melhor o contrato — uma resposta com metade dos
 * campos opcionais ausentes não diz o que a rota devolve.
 */
func gravarContrato(metodo, caminho, corpo string, w *httptest.ResponseRecorder) {
	if !aGravar() {
		return
	}

	nova := chamada{Metodo: metodo, Caminho: caminho, Status: w.Code}
	// O que o servidor **aceitou**. Uma recusa descreve o que ele não quer.
	if corpo != "" && w.Code < 400 {
		nova.Pedido = formaDe([]byte(corpo))
	}
	if forma := formaDe(w.Body.Bytes()); len(forma) > 0 {
		nova.Resposta = forma
	}
	if len(nova.Pedido) == 0 && len(nova.Resposta) == 0 {
		return
	}

	chave := metodo + " " + caminho
	gravadasMu.Lock()
	defer gravadasMu.Unlock()

	velha, existe := gravadas[chave]
	if !existe {
		gravadas[chave] = nova
		return
	}

	/*
	 * O **pedido** junta-se; a **resposta** substitui-se pela mais completa.
	 *
	 * ⚠️ Não é simetria por esquecimento. Num `PATCH`, cada teste manda um
	 * campo — `targetWeightKg` num, `targetDate` noutro, `priority` num
	 * terceiro — e todos os três são campos que o servidor aceita. Guardar só
	 * "a chamada com mais campos" ficava com um deles e escondia os outros
	 * dois: os tipos gerados daí recusariam chamadas boas.
	 *
	 * A resposta é o contrário. Ela vem inteira de cada vez, e uma com metade
	 * dos campos opcionais ausentes descreve a rota pior do que a completa —
	 * juntá-las seria inventar uma resposta que a rota nunca deu.
	 */
	junta := velha
	if junta.Pedido == nil {
		junta.Pedido = map[string]string{}
	}
	for campo, tipo := range nova.Pedido {
		junta.Pedido[campo] = tipo
	}
	if len(nova.Resposta) > len(velha.Resposta) {
		junta.Resposta = nova.Resposta
		junta.Status = nova.Status
	}
	if len(junta.Pedido) == 0 {
		junta.Pedido = nil
	}
	gravadas[chave] = junta
}

/*
 * formaDe reduz um JSON ao esqueleto: caminho de campo → tipo.
 *
 * As listas colapsam no primeiro elemento — uma lista de dez aulas tem a forma
 * de uma aula, e guardar as dez era guardar dados a fingir que era contrato.
 */
func formaDe(bruto []byte) map[string]string {
	var v any
	if err := json.Unmarshal(bruto, &v); err != nil {
		return nil
	}
	out := map[string]string{}
	descer(v, "", out)
	return out
}

func descer(v any, prefixo string, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		/*
		 * Um objecto vazio deixa rasto, como uma lista vazia.
		 *
		 * ⚠️ Sem isto ele **desaparecia** da forma: o `prices` da lista de
		 * compras chega vazio numa semana por tocar, não gravava nada, e o
		 * contrato ficava a dizer que aquele campo não existe. Um campo que o
		 * contrato não conhece é um campo que se pode apagar do servidor sem
		 * ninguém dar por isso — que é exactamente o que isto existe para
		 * apanhar.
		 */
		if len(t) == 0 && prefixo != "" {
			out[prefixo+"{}"] = "vazio"
			return
		}
		for k, val := range t {
			caminho := k
			if prefixo != "" {
				caminho = prefixo + "." + k
			}
			descer(val, caminho, out)
		}
	case []any:
		if len(t) == 0 {
			out[prefixo+"[]"] = "vazio"
			return
		}
		descer(t[0], prefixo+"[]", out)
	case string:
		out[prefixo] = "texto"
	case float64:
		out[prefixo] = "número"
	case bool:
		out[prefixo] = "booleano"
	case nil:
		out[prefixo] = "nulo"
	}
}

/*
 * escreverContrato despeja o que foi gravado, no fim da suite.
 *
 * Um ficheiro só, ordenado, para o `diff` do CI ser legível: quem mudar um
 * campo vê exactamente qual.
 */
func escreverContrato() {
	if !aGravar() {
		return
	}
	gravadasMu.Lock()
	defer gravadasMu.Unlock()

	chaves := make([]string, 0, len(gravadas))
	for k := range gravadas {
		chaves = append(chaves, k)
	}
	sort.Strings(chaves)

	lista := make([]chamada, 0, len(chaves))
	for _, k := range chaves {
		lista = append(lista, gravadas[k])
	}

	destino := filepath.Join("testdata", "contrato.json")
	bruto, err := json.MarshalIndent(lista, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(destino), 0o755)
	_ = os.WriteFile(destino, append(bruto, '\n'), 0o644)
}
