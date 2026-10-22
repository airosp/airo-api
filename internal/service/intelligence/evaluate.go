package intelligence

import (
	"context"

	core "github.com/airosp/airo-api/internal/intelligence"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"github.com/airosp/airo-api/internal/intelligence/policy"
	"github.com/airosp/airo-api/internal/intelligence/validation"
)

/*
 * Evaluate corre uma amostra pelo **mesmo caminho** de produção — as mesmas
 * instruções, o mesmo esquema, a mesma validação e o mesmo resolvedor — sem
 * base de dados, sem fila e sem consentimento.
 *
 * Existe para `cmd/avaliar-ia`. Uma avaliação que montasse o pedido de outra
 * forma mediria outra coisa.
 */
func Evaluate(ctx context.Context, provider core.Provider, text string, image []byte, mime string) (validation.ResolveResult, core.StructuredResult, error) {
	cat, err := validation.LoadCatalog()
	if err != nil {
		return validation.ResolveResult{}, core.StructuredResult{}, err
	}
	schema := contracts.MealExtractionSchema(cat.FoodIDs(), cat.RecipeIDs())
	var (
		res    core.StructuredResult
		source string
	)
	in := jobInput{Slot: "lunch"}
	if image != nil {
		source = validation.SourcePhoto
		res, err = provider.AnalyzeImage(ctx, photoRequest(cat, schema, in, image, mime))
	} else {
		source = validation.SourceText
		in.Text = policy.RedactText(text)
		res, err = provider.GenerateStructured(ctx, textRequest(cat, schema, in))
	}
	if err != nil {
		return validation.ResolveResult{}, res, err
	}
	ex, err := validation.ParseMealExtraction(res.Data)
	if err != nil {
		return validation.ResolveResult{}, res, err
	}
	return cat.Resolve(validation.ResolveInput{Source: source, Extraction: ex, UserText: in.Text}), res, nil
}
