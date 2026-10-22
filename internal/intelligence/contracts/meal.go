// Package contracts são as formas que atravessam a camada de inteligência: o
// que se pede ao modelo, o que ele pode devolver, e o rascunho que a pessoa vê.
//
// Versionadas. Mudar um campo é mudar `SchemaVersion`, e uma proposta guardada
// com a versão antiga continua a ler-se com a forma com que foi escrita.
package contracts

import (
	"encoding/json"
	"sort"
)

// SchemaVersion é a versão do contrato das refeições.
const SchemaVersion = "1.0"

// Tarefas.
const (
	TaskMealPhoto = "meal_photo"
	TaskMealText  = "meal_text"
)

// Estados de uma execução. A transição para `applied` nunca vem do modelo: vem
// de alguém carregar em "confirmar", e de a confirmação voltar a validar tudo.
const (
	JobQueued          = "queued"
	JobProcessing      = "processing"
	JobValidating      = "validating"
	JobReadyForReview  = "ready_for_review"
	JobNeedsCorrection = "needs_correction"
	JobApplied         = "applied"
	JobDismissed       = "dismissed"
	JobFailed          = "failed"
	JobCancelled       = "cancelled"
	JobExpired         = "expired"
)

// Terminal diz se o estado já não muda sozinho.
func Terminal(status string) bool {
	switch status {
	case JobQueued, JobProcessing, JobValidating:
		return false
	}
	return true
}

// Unidades que o modelo pode ler num texto. Fechadas: uma unidade inventada
// ("uma mão-cheia") não tem conversão, e cai em `none`.
const (
	UnitGram    = "g"
	UnitKilo    = "kg"
	UnitMl      = "ml"
	UnitLitre   = "l"
	UnitPiece   = "unit"
	UnitServing = "serving"
	UnitNone    = "none"
)

// NoMatch é o valor que o modelo usa quando o alimento não está no catálogo.
// Um enum não aceita nulo e um texto livre aceitaria um identificador
// inventado — esta é a terceira opção, e é explícita.
const NoMatch = "none"

/*
 * MealExtraction é o que o modelo devolve.
 *
 * ⚠️ **Sem calorias e sem macros.** Não há campo para isso, e é de propósito:
 * se o modelo não as pode escrever, ninguém as pode copiar por engano para um
 * registo. Os números saem do catálogo, pelo motor de nutrição, a partir do
 * que a pessoa confirmou.
 */
type MealExtraction struct {
	ContainsFood bool            `json:"containsFood"`
	Items        []ExtractedItem `json:"items"`
}

type ExtractedItem struct {
	// Label é como o modelo chama ao que viu ou leu. Só se mostra; nunca decide.
	Label string `json:"label"`
	// FoodID é um identificador do catálogo, ou `none`.
	FoodID string `json:"foodId"`
	// RecipeID é uma receita do catálogo, ou `none`. Uma receita expande-se nos
	// seus ingredientes do lado do servidor.
	RecipeID string `json:"recipeId"`
	// Quantity é a quantidade **dita** pela pessoa. Só no texto.
	Quantity Quantity `json:"quantity"`
	// EstimatedGrams é a estimativa visual. Só na fotografia.
	EstimatedGrams *float64 `json:"estimatedGrams"`
}

type Quantity struct {
	Amount *float64 `json:"amount"`
	Unit   string   `json:"unit"`
}

// MealExtractionSchema devolve o JSON Schema da resposta.
//
// Os identificadores entram como `enum`. É o que faz o modelo **não poder**
// devolver um alimento que não existe: com saídas estruturadas, um valor fora
// da lista não é gerado. A validação do servidor confirma-o na mesma, porque
// "não pode" do fornecedor é uma promessa, e a nossa base de dados não se
// escreve com promessas.
func MealExtractionSchema(foodIDs, recipeIDs []string) json.RawMessage {
	foods := append(sortedCopy(foodIDs), NoMatch)
	recipes := append(sortedCopy(recipeIDs), NoMatch)

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"containsFood", "items"},
		"properties": map[string]any{
			"containsFood": map[string]any{"type": "boolean"},
			// Sem `maxItems`: nem todos os modelos o aceitam em modo estrito, e
			// o tecto é do servidor de qualquer forma — ver `MaxItems`.
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"label", "foodId", "recipeId", "quantity", "estimatedGrams"},
					"properties": map[string]any{
						"label":    map[string]any{"type": "string"},
						"foodId":   map[string]any{"type": "string", "enum": foods},
						"recipeId": map[string]any{"type": "string", "enum": recipes},
						"quantity": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"required":             []string{"amount", "unit"},
							"properties": map[string]any{
								"amount": map[string]any{"type": []string{"number", "null"}},
								"unit": map[string]any{"type": "string", "enum": []string{
									UnitGram, UnitKilo, UnitMl, UnitLitre, UnitPiece, UnitServing, UnitNone,
								}},
							},
						},
						"estimatedGrams": map[string]any{"type": []string{"number", "null"}},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(schema)
	return raw
}

// MaxItems é o tecto de alimentos numa refeição. Um prato com vinte coisas não
// é um prato — é um modelo a enumerar o que não vê.
const MaxItems = 12

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// ── o rascunho que a pessoa revê ───────────────────────────────────────────

// Origem das gramas de um item.
const (
	GramsEstimated = "estimated" // a olho, pela fotografia
	GramsDeclared  = "declared"  // ditas pela pessoa
	GramsMissing   = "missing"   // ninguém disse — fica para a pessoa
)

// Avisos. São códigos e não frases: o texto vive na app, na língua dela, e um
// aviso escrito pelo modelo seria texto livre num ecrã de saúde.
const (
	WarnPortionsEstimated = "PORTIONS_ESTIMATED"
	WarnQuantitiesMissing = "QUANTITIES_MISSING"
	WarnUnknownItems      = "UNKNOWN_ITEMS"
	WarnNoFood            = "NO_FOOD_RECOGNIZED"
	WarnTooManyItems      = "TOO_MANY_ITEMS"
)

// MealDraft é a proposta: o que foi reconhecido, à espera de confirmação.
type MealDraft struct {
	SchemaVersion string       `json:"schemaVersion"`
	Source        string       `json:"source"` // "photo" | "text"
	Slot          string       `json:"slot"`
	LocalDay      string       `json:"localDay"`
	PhotoURL      string       `json:"photoUrl,omitempty"`
	PhotoThumbURL string       `json:"photoThumbUrl,omitempty"`
	Items         []DraftItem  `json:"items"`
	UnknownItems  []UnknownRef `json:"unknownItems"`
	Warnings      []string     `json:"warnings"`
}

type DraftItem struct {
	Label    string `json:"label"`
	FoodID   string `json:"foodId"`
	FoodName string `json:"foodName"`
	// Grams é nulo quando ninguém disse quanto: a pessoa escreve, ou aceita a
	// porção habitual. Inventar um número aqui era o modelo a decidir.
	Grams            *float64 `json:"grams"`
	GramsSource      string   `json:"gramsSource"`
	SuggestedGrams   float64  `json:"suggestedGrams"`
	PortionUncertain bool     `json:"portionUncertain"`
	// FromRecipe diz de que receita veio, quando veio de uma.
	FromRecipe string  `json:"fromRecipe,omitempty"`
	Per100g    Per100g `json:"per100g"`
}

// Per100g vem do catálogo, nunca do modelo. Está aqui para a app recalcular
// enquanto a pessoa mexe nas gramas, sem uma ida ao servidor por cada toque.
type Per100g struct {
	Kcal    float64 `json:"kcal"`
	Protein float64 `json:"protein"`
	Carbs   float64 `json:"carbs"`
	Fat     float64 `json:"fat"`
}

type UnknownRef struct {
	Label string `json:"label"`
}
