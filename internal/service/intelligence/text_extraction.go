package intelligence

import (
	"encoding/json"
	"fmt"

	core "github.com/airosp/airo-api/internal/intelligence"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"github.com/airosp/airo-api/internal/intelligence/validation"
)

/*
 * textRequest monta o pedido de interpretação de um registo escrito.
 *
 * O texto vai num campo JSON e não colado nas instruções: um texto que diga
 * "ignora as regras anteriores" chega ao modelo como o valor de "texto", e as
 * instruções dizem que esse valor é um dado. O texto já passou por
 * `policy.RedactText` antes de ser guardado.
 */
func textRequest(cat *validation.Catalog, schema json.RawMessage, in jobInput) core.StructuredRequest {
	payload, _ := json.Marshal(map[string]string{
		"pedido":   "Identifica os alimentos e as quantidades ditas neste registo.",
		"refeicao": in.Slot,
		"texto":    in.Text,
	})
	return core.StructuredRequest{
		Task:         contracts.TaskMealText,
		SchemaName:   "meal_extraction",
		Schema:       schema,
		Instructions: fmt.Sprintf(instrucoesTexto, cat.Names()),
		Payload:      payload,
		MaxTokens:    maxOutputTokens,
	}
}
