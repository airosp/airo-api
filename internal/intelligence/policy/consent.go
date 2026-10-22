// Package policy decide se uma operação de inteligência pode correr: se há
// autorização, se a funcionalidade está ligada, se há orçamento, e o que sai
// do pedido antes de ele deixar a Airo.
package policy

import "github.com/airosp/airo-api/internal/intelligence/contracts"

// Finalidades. Uma autorização por finalidade, e não uma genérica: deixar
// analisar fotografias de refeições não é deixar ler o progresso.
const (
	PurposeMealPhoto       = "meal_photo"
	PurposeMealText        = "meal_text"
	PurposeProgress        = "progress"
	PurposeSpecialistShare = "specialist_share"
)

/*
 * PolicyVersions é a versão do texto que a pessoa lê antes de autorizar.
 *
 * ⚠️ Mudar o texto da app sem mudar a versão aqui é ficar com autorizações
 * dadas a um texto que já não existe. Mudar a versão obriga a pedir outra vez:
 * uma autorização com a versão antiga deixa de valer.
 */
var PolicyVersions = map[string]string{
	PurposeMealPhoto: "2026-10-22",
	PurposeMealText:  "2026-10-22",
}

// KnownPurpose diz se a finalidade existe e tem texto publicado.
func KnownPurpose(p string) bool {
	_, ok := PolicyVersions[p]
	return ok
}

// PurposeFor é a finalidade de que uma tarefa precisa.
func PurposeFor(task string) string {
	switch task {
	case contracts.TaskMealPhoto:
		return PurposeMealPhoto
	case contracts.TaskMealText:
		return PurposeMealText
	}
	return ""
}

// ConsentValid diz se uma autorização guardada ainda vale para o texto actual.
func ConsentValid(purpose, grantedVersion string) bool {
	v, ok := PolicyVersions[purpose]
	return ok && v == grantedVersion
}
