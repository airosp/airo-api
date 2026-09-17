package nutrition

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

/*
 * O que procurar para mostrar cada alimento.
 *
 * ⚠️ **Consultas curadas, não o nome do alimento.** Procurar "Xima" ou "Matapa"
 * devolve lixo: são nomes que o acervo não conhece em português. Cada alimento
 * tem uma consulta escrita à mão em inglês, e descritiva do **prato** e não do
 * ingrediente cru — "cooked white rice bowl" e não "rice".
 *
 * A lista vive aqui e não no telemóvel porque escolher o que procurar é uma
 * decisão, e as decisões são do servidor. Enquanto vivia lá, cada app tinha a
 * sua cópia e duas versões diferentes procuravam coisas diferentes para o mesmo
 * alimento.
 */

//go:embed data/food_photo_queries.json
var consultasBrutas []byte

var (
	consultasUmaVez sync.Once
	consultas       map[string]string
)

// ConsultaDoAlimento é o que se procura para este alimento. Vazio = não se
// adivinha, e o ecrã fica com o gradiente da categoria.
func ConsultaDoAlimento(foodID string) string {
	consultasUmaVez.Do(func() {
		consultas = map[string]string{}
		_ = json.Unmarshal(consultasBrutas, &consultas)
	})
	return consultas[foodID]
}

/*
 * ConsultaDeEtiqueta é o que se procura para texto escrito pela pessoa.
 *
 * Aqui a consulta é a própria etiqueta: o acervo responde bem a português
 * ("pastel de nata" devolve pastéis de nata), e como é texto livre não há
 * consulta curada possível. Normaliza-se para "bolo de chocolate" e
 * "Bolo De Chocolate " partilharem a mesma entrada.
 *
 * Etiquetas muito curtas não se procuram: dão resultados aleatórios, e uma
 * imagem aleatória ao lado do que a pessoa comeu é pior do que imagem nenhuma.
 */
func ConsultaDeEtiqueta(etiqueta string) string {
	limpa := strings.Join(strings.Fields(strings.ToLower(etiqueta)), " ")
	if len(limpa) < 3 {
		return ""
	}
	return limpa
}
