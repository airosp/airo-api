// Package validation é a porta por onde a resposta do modelo entra na Airo.
//
// Nada do que o modelo devolve é usado sem passar por aqui: a forma é lida de
// forma estrita, os identificadores são confrontados com o catálogo, as
// quantidades com o que a pessoa escreveu, e o texto livre é limpo antes de
// chegar a um ecrã.
package validation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/airosp/airo-api/internal/intelligence/contracts"
)

// ErrInvalidOutput é uma resposta que não tem a forma prometida.
var ErrInvalidOutput = errors.New("resposta do modelo fora do contrato")

// ParseMealExtraction lê a resposta do modelo.
//
// Estrita: um campo a mais é recusado. As saídas estruturadas garantem a forma
// quando a geração corre como esperado — e é precisamente quando não corre que
// esta verificação importa.
func ParseMealExtraction(data []byte) (contracts.MealExtraction, error) {
	var out contracts.MealExtraction
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if dec.More() {
		return out, fmt.Errorf("%w: conteúdo depois do objecto", ErrInvalidOutput)
	}
	if out.Items == nil {
		return out, fmt.Errorf("%w: falta a lista de itens", ErrInvalidOutput)
	}
	for i, it := range out.Items {
		if it.EstimatedGrams != nil && !finite(*it.EstimatedGrams) {
			return out, fmt.Errorf("%w: item %d com gramas inválidas", ErrInvalidOutput, i)
		}
		if it.Quantity.Amount != nil && !finite(*it.Quantity.Amount) {
			return out, fmt.Errorf("%w: item %d com quantidade inválida", ErrInvalidOutput, i)
		}
	}
	return out, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
