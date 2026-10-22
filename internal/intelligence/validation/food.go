package validation

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"golang.org/x/text/unicode/norm"
)

// Catalog é o que o resolvedor precisa de saber sobre a base de alimentos.
type Catalog struct {
	foods   map[string]nutrition.Food
	recipes map[string]nutrition.Recipe
	// byName ordenado do nome mais comprido para o mais curto: "peixe seco"
	// tem de ganhar a "peixe" quando o rótulo diz "peixe seco frito".
	byName []nameEntry
}

type nameEntry struct {
	norm string
	id   string
}

// LoadCatalog lê o catálogo do motor de nutrição — o mesmo que monta o plano.
func LoadCatalog() (*Catalog, error) {
	foods, err := nutrition.Foods()
	if err != nil {
		return nil, err
	}
	recipes, err := nutrition.Recipes()
	if err != nil {
		return nil, err
	}
	c := &Catalog{foods: map[string]nutrition.Food{}, recipes: map[string]nutrition.Recipe{}}
	for _, f := range foods {
		c.foods[f.ID] = f
		c.byName = append(c.byName, nameEntry{norm: normalize(f.Name), id: f.ID})
	}
	for _, r := range recipes {
		c.recipes[r.ID] = r
	}
	// Ordenação estável por comprimento, sem depender de `sort` com empates.
	for i := 1; i < len(c.byName); i++ {
		for j := i; j > 0 && len(c.byName[j].norm) > len(c.byName[j-1].norm); j-- {
			c.byName[j], c.byName[j-1] = c.byName[j-1], c.byName[j]
		}
	}
	return c, nil
}

// FoodIDs e RecipeIDs são os valores que o esquema da resposta aceita.
func (c *Catalog) FoodIDs() []string {
	out := make([]string, 0, len(c.foods))
	for id := range c.foods {
		out = append(out, id)
	}
	return out
}

func (c *Catalog) RecipeIDs() []string {
	out := make([]string, 0, len(c.recipes))
	for id := range c.recipes {
		out = append(out, id)
	}
	return out
}

// Food devolve um alimento do catálogo.
func (c *Catalog) Food(id string) (nutrition.Food, bool) {
	f, ok := c.foods[id]
	return f, ok
}

// Names é a lista "identificador: nome" que vai nas instruções do modelo, para
// ele saber o que cada identificador quer dizer.
func (c *Catalog) Names() string {
	var b strings.Builder
	for _, e := range c.byName {
		b.WriteString(e.id + ": " + c.foods[e.id].Name + "\n")
	}
	// Por ordem fixa: as mesmas instruções a cada pedido são instruções que o
	// fornecedor pode guardar em cache.
	ids := c.RecipeIDs()
	sort.Strings(ids)
	for _, id := range ids {
		b.WriteString("receita " + id + ": " + c.recipes[id].Name + "\n")
	}
	return b.String()
}

/*
 * Gramas por unidade dos alimentos que se contam.
 *
 * "Dois ovos" é uma quantidade dita, e converte-se. "Duas porções de arroz"
 * também, pela porção habitual do catálogo. Mas "dois arroz" não quer dizer
 * nada — e por isso só os alimentos que se contam têm unidade. O resto fica
 * por preencher, que é melhor do que um número inventado.
 *
 * ⚠️ São médias, não pesagens. Um item convertido daqui fica marcado como
 * porção incerta.
 */
var gramsPerUnit = map[string]float64{
	"eggs": 50, "banana": 120, "orange": 150, "mango": 200, "avocado": 150,
	"bread": 40, "sweet_potato": 150, "potato": 150, "plantain": 180,
	"papaya": 300, "pineapple": 900, "coconut": 400,
}

// Source é de onde veio a refeição.
const (
	SourcePhoto = "photo"
	SourceText  = "text"
)

// ResolveInput é o que o resolvedor recebe.
type ResolveInput struct {
	Source     string
	Extraction contracts.MealExtraction
	// UserText é o texto que a pessoa escreveu, já limpo. É contra ele que se
	// confirmam as quantidades: uma quantidade que não está no texto foi
	// inventada, e cai.
	UserText string
}

// ResolveResult é o rascunho e o estado que lhe cabe.
type ResolveResult struct {
	Items    []contracts.DraftItem
	Unknown  []contracts.UnknownRef
	Warnings []string
	Status   string
}

/*
 * Resolve transforma o que o modelo disse num rascunho que se pode confirmar.
 *
 * Cada item acaba de uma de três formas: um alimento do catálogo com gramas,
 * um alimento do catálogo sem gramas (a pessoa preenche), ou um desconhecido
 * (mostra-se o nome, e a pessoa escolhe). Nunca um alimento que o catálogo não
 * tenha, e nunca calorias.
 */
func (c *Catalog) Resolve(in ResolveInput) ResolveResult {
	res := ResolveResult{Items: []contracts.DraftItem{}, Unknown: []contracts.UnknownRef{}, Warnings: []string{}}
	textNumbers := numbersIn(in.UserText)

	extracted := in.Extraction.Items
	if len(extracted) > contracts.MaxItems {
		extracted = extracted[:contracts.MaxItems]
		res.Warnings = append(res.Warnings, contracts.WarnTooManyItems)
	}

	for _, ex := range extracted {
		label := SanitizeLabel(ex.Label)

		if r, ok := c.recipes[ex.RecipeID]; ok {
			res.Items = append(res.Items, c.expandRecipe(r, label, ex, in.Source, textNumbers)...)
			continue
		}

		foodID := ex.FoodID
		if _, ok := c.foods[foodID]; !ok {
			// O modelo não reconheceu, ou devolveu o que não existe: tenta-se
			// pelo nome, de forma determinista, e só depois se desiste.
			foodID = c.matchByName(label)
		}
		food, ok := c.foods[foodID]
		if !ok {
			if label != "" {
				res.Unknown = append(res.Unknown, contracts.UnknownRef{Label: label})
			}
			continue
		}
		if label == "" {
			label = food.Name
		}
		item := contracts.DraftItem{
			Label: label, FoodID: food.ID, FoodName: food.Name,
			SuggestedGrams: food.Serving, Per100g: per100g(food),
		}
		switch in.Source {
		case SourcePhoto:
			if g := ex.EstimatedGrams; g != nil && validGrams(*g) {
				v := round1(*g)
				item.Grams, item.GramsSource = &v, contracts.GramsEstimated
			} else {
				item.GramsSource = contracts.GramsMissing
			}
			// Uma porção vista numa fotografia é sempre uma estimativa.
			item.PortionUncertain = true
		default:
			if g, uncertain, ok := declaredGrams(ex.Quantity, food, textNumbers); ok {
				item.Grams, item.GramsSource, item.PortionUncertain = &g, contracts.GramsDeclared, uncertain
			} else {
				item.GramsSource = contracts.GramsMissing
			}
		}
		res.Items = append(res.Items, item)
	}

	if len(res.Items) > contracts.MaxItems {
		res.Items = res.Items[:contracts.MaxItems]
		if !contains(res.Warnings, contracts.WarnTooManyItems) {
			res.Warnings = append(res.Warnings, contracts.WarnTooManyItems)
		}
	}

	switch {
	case len(res.Items) == 0 && len(res.Unknown) == 0, !in.Extraction.ContainsFood && len(res.Items) == 0:
		res.Status = contracts.JobNeedsCorrection
		res.Warnings = append(res.Warnings, contracts.WarnNoFood)
	case len(res.Items) == 0:
		res.Status = contracts.JobNeedsCorrection
		res.Warnings = append(res.Warnings, contracts.WarnUnknownItems)
	default:
		res.Status = contracts.JobReadyForReview
		if in.Source == SourcePhoto {
			res.Warnings = append(res.Warnings, contracts.WarnPortionsEstimated)
		}
		for _, it := range res.Items {
			if it.Grams == nil {
				res.Warnings = append(res.Warnings, contracts.WarnQuantitiesMissing)
				break
			}
		}
		if len(res.Unknown) > 0 {
			res.Warnings = append(res.Warnings, contracts.WarnUnknownItems)
		}
	}
	return res
}

/*
 * expandRecipe abre uma receita nos seus ingredientes.
 *
 * Os ingredientes são o que se regista, porque é deles que saem os números. A
 * receita diz as proporções; a quantidade sai do que a fotografia mostrou ou do
 * que a pessoa disse ("dois pratos de matapa com xima"). Sem nenhuma das duas,
 * as gramas ficam por preencher e a receita dá a sugestão.
 */
func (c *Catalog) expandRecipe(r nutrition.Recipe, label string, ex contracts.ExtractedItem, source string, textNumbers map[float64]bool) []contracts.DraftItem {
	var total float64
	for _, ing := range r.Ingredients {
		total += ing.Grams
	}
	serves := float64(r.Serves)
	if serves < 1 {
		serves = 1
	}
	perServing := total / serves

	factor, gramsSource, uncertain := 0.0, contracts.GramsMissing, true
	switch source {
	case SourcePhoto:
		if g := ex.EstimatedGrams; g != nil && validGrams(*g) && perServing > 0 {
			factor, gramsSource = *g/perServing, contracts.GramsEstimated
		}
	default:
		q := ex.Quantity
		if q.Amount != nil && *q.Amount > 0 && amountInText(*q.Amount, textNumbers) {
			switch q.Unit {
			case contracts.UnitServing, contracts.UnitPiece:
				factor, gramsSource = *q.Amount, contracts.GramsDeclared
			case contracts.UnitGram:
				factor, gramsSource, uncertain = *q.Amount/perServing, contracts.GramsDeclared, false
			case contracts.UnitKilo:
				factor, gramsSource, uncertain = *q.Amount*1000/perServing, contracts.GramsDeclared, false
			}
		}
	}

	if label == "" {
		label = r.Name
	}
	out := make([]contracts.DraftItem, 0, len(r.Ingredients))
	for _, ing := range r.Ingredients {
		food, ok := c.foods[ing.FoodID]
		if !ok {
			continue
		}
		suggested := round1(ing.Grams / serves)
		item := contracts.DraftItem{
			Label: food.Name, FoodID: food.ID, FoodName: food.Name,
			SuggestedGrams: suggested, PortionUncertain: uncertain,
			FromRecipe: r.ID, Per100g: per100g(food),
		}
		item.GramsSource = gramsSource
		if gramsSource != contracts.GramsMissing {
			g := round1(suggested * factor)
			if validGrams(g) {
				item.Grams = &g
			} else {
				item.GramsSource = contracts.GramsMissing
			}
		}
		out = append(out, item)
	}
	return out
}

// declaredGrams converte a quantidade dita em gramas, se ela estiver no texto.
func declaredGrams(q contracts.Quantity, food nutrition.Food, textNumbers map[float64]bool) (float64, bool, bool) {
	if q.Amount == nil || *q.Amount <= 0 || !amountInText(*q.Amount, textNumbers) {
		return 0, false, false
	}
	a := *q.Amount
	var g float64
	uncertain := false
	switch q.Unit {
	case contracts.UnitGram:
		g = a
	case contracts.UnitKilo:
		g = a * 1000
	case contracts.UnitMl:
		// Mililitros por gramas: verdade para a água e o leite, aproximação
		// para o resto. Fica marcado.
		g, uncertain = a, true
	case contracts.UnitLitre:
		g, uncertain = a*1000, true
	case contracts.UnitPiece:
		per, ok := gramsPerUnit[food.ID]
		if !ok {
			return 0, false, false
		}
		g, uncertain = a*per, true
	case contracts.UnitServing:
		g, uncertain = a*food.Serving, true
	default:
		return 0, false, false
	}
	g = round1(g)
	if !validGrams(g) {
		return 0, false, false
	}
	return g, uncertain, true
}

func (c *Catalog) matchByName(label string) string {
	n := normalize(label)
	if n == "" {
		return ""
	}
	padded := " " + n + " "
	for _, e := range c.byName {
		if strings.Contains(padded, " "+e.norm+" ") {
			return e.id
		}
	}
	return ""
}

func per100g(f nutrition.Food) contracts.Per100g {
	return contracts.Per100g{Kcal: f.Kcal, Protein: f.Macros.Protein, Carbs: f.Macros.Carbs, Fat: f.Macros.Fat}
}

func validGrams(g float64) bool { return g > 0 && g <= nutrition.MaxLoggedGrams }

func round1(v float64) float64 {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	out, _ := strconv.ParseFloat(s, 64)
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// normalize tira acentos e maiúsculas: "Feijão" e "feijao" são o mesmo.
func normalize(s string) string {
	decomposed := norm.NFD.String(strings.ToLower(s))
	var b strings.Builder
	for _, r := range decomposed {
		if r >= 0x300 && r <= 0x36f {
			continue
		}
		if r == '-' {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var numberRe = regexp.MustCompile(`\d+(?:[.,]\d+)?`)

// As quantidades por extenso que aparecem num registo. "Meio" e "meia" são as
// únicas fracções que se dizem assim.
var numberWords = map[string]float64{
	"um": 1, "uma": 1, "dois": 2, "duas": 2, "tres": 3, "quatro": 4, "cinco": 5,
	"seis": 6, "sete": 7, "oito": 8, "nove": 9, "dez": 10, "meio": 0.5, "meia": 0.5,
}

// numbersIn devolve os números que aparecem no texto, em algarismos e por
// extenso.
func numbersIn(text string) map[float64]bool {
	out := map[float64]bool{}
	for _, m := range numberRe.FindAllString(text, -1) {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", "."), 64); err == nil {
			out[v] = true
		}
	}
	for _, w := range strings.Fields(normalize(text)) {
		w = strings.Trim(w, ".,;:!?()")
		if v, ok := numberWords[w]; ok {
			out[v] = true
		}
	}
	return out
}

// amountInText diz se a quantidade está no texto — a prova de que foi dita, e
// não inventada. Um quilo e meio pode vir como "1,5 kg" ou "1500 g", e por
// isso aceitam-se também os múltiplos de mil.
func amountInText(a float64, nums map[float64]bool) bool {
	if nums[a] {
		return true
	}
	return nums[a*1000] || nums[a/1000]
}
