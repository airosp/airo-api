package intelligence

import (
	"encoding/json"
	"fmt"

	core "github.com/airosp/airo-api/internal/intelligence"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"github.com/airosp/airo-api/internal/intelligence/validation"
)

/*
 * photoRequest monta o pedido de reconhecimento de uma fotografia.
 *
 * O que sai da Airo: as instruções, o catálogo, a refeição do dia em que a
 * fotografia foi tirada, e a imagem já reduzida e sem metadados. Não sai o
 * nome, o telefone, o identificador de quem a tirou, nem o endereço onde ela
 * está guardada.
 */
func photoRequest(cat *validation.Catalog, schema json.RawMessage, in jobInput, image []byte, mime string) core.ImageRequest {
	payload, _ := json.Marshal(map[string]string{
		"pedido":   "Identifica os alimentos desta fotografia de uma refeição.",
		"refeicao": in.Slot,
	})
	return core.ImageRequest{
		StructuredRequest: core.StructuredRequest{
			Task:         contracts.TaskMealPhoto,
			SchemaName:   "meal_extraction",
			Schema:       schema,
			Instructions: fmt.Sprintf(instrucoesFoto, cat.Names()),
			Payload:      payload,
			MaxTokens:    maxOutputTokens,
		},
		Image: image,
		MIME:  mime,
	}
}
