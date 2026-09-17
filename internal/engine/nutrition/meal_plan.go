package nutrition

import (
	"fmt"
	"math"
	"strings"

	"github.com/airosp/airo-api/internal/engine/portable"
)

// Monta as refeições do dia a partir de alimentos reais — respeitando estilo,
// orçamento e exclusões, e fechando as contas com o alvo do dia.
//
// Porte de `mobile/modules/nutrition-engine/engines/meal-engine.ts`. Os macros
// andam em vírgula flutuante com uma casa (`MacrosFloat`) e não em gramas
// inteiras: o `Macros` inteiro é o alvo do **dia**, e arredondar a cada refeição
// afastava a soma do total.

// SlotTitles — o nome que a pessoa lê no cartão.
var SlotTitles = map[Slot]string{
	Breakfast: "Pequeno-almoço",
	Lunch:     "Almoço",
	Snack:     "Lanche",
	Dinner:    "Jantar",
	Supper:    "Ceia",
}

// Os papéis de uma refeição. Não é decoração: é o que desloca os macros.
const (
	RoleBalanced    = "balanced"
	RolePreWorkout  = "pre_workout"
	RolePostWorkout = "post_workout"
	RoleLight       = "light"
)

type PlannedMeal struct {
	ID     string      `json:"id"`
	Slot   Slot        `json:"slot"`
	Title  string      `json:"title"`
	Role   string      `json:"role"`
	Kcal   int         `json:"kcal"`
	Macros MacrosFloat `json:"macros"`
	Items  []MealItem  `json:"items"`
}

// TrainingLoad é o que a nutrição precisa de saber sobre o treino: quantas
// sessões por semana e a que horas. Não precisa de saber qual é o treino.
type TrainingLoad struct {
	SessionsPerWeek int
	WorkoutTime     string
}

type BuildMealsInput struct {
	DayISO        string
	CalorieTarget int
	Macros        MacrosFloat
	Diet          DietProfile
	Training      TrainingLoad
	TrainsToday   bool
}

// BuildMeals monta o dia inteiro.
func BuildMeals(c Config, in BuildMealsInput) ([]PlannedMeal, error) {
	slots := SlotsFor(c, in.Diet.MealsPerDay)
	parts := SplitCalories(c, in.CalorieTarget, in.Diet.MealsPerDay)
	// A semente vem do dia, para o plano variar sem ser imprevisível.
	seed := seedFromDay(in.DayISO)

	out := make([]PlannedMeal, 0, len(slots))
	for i, slot := range slots {
		if i >= len(parts) {
			break
		}
		share := 0.0
		if in.CalorieTarget > 0 {
			share = float64(parts[i]) / float64(in.CalorieTarget)
		}
		role := roleFor(c, slot, in.TrainsToday && in.Training.SessionsPerWeek > 0, in.Training.WorkoutTime)
		mealMacros := shiftForRole(c, scaleMacrosFloat(in.Macros, share), role, float64(parts[i]))

		items, err := buildMeal(buildMealInput{
			Slot: slot, Kcal: float64(parts[i]), Macros: mealMacros,
			Diet: in.Diet, Role: role, Seed: seed + i, AllowRecipe: true,
		})
		if err != nil {
			return nil, err
		}

		out = append(out, PlannedMeal{
			ID:    fmt.Sprintf("%s-%s", in.DayISO, slot),
			Slot:  slot,
			Title: SlotTitles[slot],
			Role:  role,
			// O alvo da refeição manda: é a soma dos alvos que fecha o dia.
			Kcal:   parts[i],
			Macros: mealMacros,
			Items:  items,
		})
	}
	return out, nil
}

// seedFromDay — os últimos quatro dígitos da data.
//
// Porte de `Number(dayISO.replace(/\D/g, ”).slice(-4)) || 0`. Uma data
// incompleta dá 0, e 0 é uma semente válida: escolhe o primeiro de cada lista.
func seedFromDay(dayISO string) int {
	var digits strings.Builder
	for _, r := range dayISO {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	s := digits.String()
	if len(s) > 4 {
		s = s[len(s)-4:]
	}
	seed := 0
	for _, r := range s {
		seed = seed*10 + int(r-'0')
	}
	return seed
}

func roleFor(c Config, slot Slot, trainsToday bool, workoutTime string) string {
	if !trainsToday || workoutTime == "" {
		return semTreino(slot)
	}
	around, ok := c.WorkoutMeals[workoutTime]
	if !ok {
		return semTreino(slot)
	}
	switch slot {
	case around.Pre:
		return RolePreWorkout
	case around.Post:
		return RolePostWorkout
	}
	return semTreino(slot)
}

func semTreino(slot Slot) string {
	if slot == Supper {
		return RoleLight
	}
	return RoleBalanced
}

// shiftForRole desloca os macros da refeição mantendo as calorias.
//
// Distinto de `ShiftMacros`: aquele renormaliza para as calorias dos macros que
// recebe e devolve gramas inteiras; este renormaliza para o alvo **da refeição**
// e guarda uma casa decimal. Chamar um pelo outro afasta a soma do dia.
func shiftForRole(c Config, macros MacrosFloat, role string, targetKcal float64) MacrosFloat {
	shift, ok := c.RoleMacroShift[role]
	if !ok {
		return macros
	}

	protein := macros.Protein * shift.Protein
	carbs := macros.Carbs * shift.Carbs
	fat := macros.Fat * shift.Fat

	kcal := protein*kcalPerGramProtein + carbs*kcalPerGramCarbs + fat*kcalPerGramFat
	factor := 1.0
	if kcal > 0 {
		factor = targetKcal / kcal
	}
	return MacrosFloat{
		Protein: portable.RoundTo(protein*factor, 1),
		Carbs:   portable.RoundTo(carbs*factor, 1),
		Fat:     portable.RoundTo(fat*factor, 1),
	}
}

func scaleMacrosFloat(m MacrosFloat, factor float64) MacrosFloat {
	return MacrosFloat{
		Protein: portable.RoundTo(m.Protein*factor, 1),
		Carbs:   portable.RoundTo(m.Carbs*factor, 1),
		Fat:     portable.RoundTo(m.Fat*factor, 1),
	}
}

// pickOne é a rotação determinística do motor de nutrição: `seed % n`.
//
// ⚠️ Não é o `portable.Pick` do treino, que salta de 13 em 13 e devolve vários.
// Aqui o passo tem de ser 1 — é o que a remontagem de uma refeição usa para
// propor outra composição.
func pickOne[T any](options []T, seed int) (T, bool) {
	var zero T
	if len(options) == 0 {
		return zero, false
	}
	return options[portable.Mod(seed, len(options))], true
}

func toItem(food Food, grams float64) MealItem {
	factor := grams / 100
	return MealItem{
		FoodID: food.ID,
		Name:   food.Name,
		Grams:  portable.RoundJS(grams),
		Kcal:   portable.RoundJS(food.Kcal * factor),
		Macros: MacrosFloat{
			Protein: portable.RoundTo(food.Macros.Protein*factor, 1),
			Carbs:   portable.RoundTo(food.Macros.Carbs*factor, 1),
			Fat:     portable.RoundTo(food.Macros.Fat*factor, 1),
		},
	}
}

// bestRecipe — entre as possíveis, a que melhor serve o alvo de proteína.
//
// A semente entra como desempate, para os dias variarem sem o plano deixar de
// cumprir os macros.
func bestRecipe(candidates []Recipe, targetKcal, targetProtein float64, seed int) (Recipe, bool) {
	if len(candidates) == 0 {
		return Recipe{}, false
	}
	if targetProtein <= 0 {
		return pickOne(candidates, seed)
	}

	gaps := make([]float64, len(candidates))
	best := math.Inf(1)
	for i, r := range candidates {
		n := ComputeRecipeNutrition(r)
		factor := 1.0
		if n.Kcal > 0 {
			factor = math.Min(1.5, math.Max(0.6, targetKcal/n.Kcal))
		}
		protein := n.Macros.Protein * factor
		gaps[i] = math.Abs(protein-targetProtein) / targetProtein
		if gaps[i] < best {
			best = gaps[i]
		}
	}

	// Todas as que ficam a 10 pontos percentuais da melhor servem; a semente
	// escolhe entre elas.
	acceptable := make([]Recipe, 0, len(candidates))
	for i, r := range candidates {
		if gaps[i] <= best+0.1 {
			acceptable = append(acceptable, r)
		}
	}
	return pickOne(acceptable, seed)
}

func fromRecipe(r Recipe, targetKcal float64) []MealItem {
	n := ComputeRecipeNutrition(r)
	factor := 1.0
	if n.Kcal > 0 {
		factor = math.Min(1.5, math.Max(0.6, targetKcal/n.Kcal))
	}
	items := make([]MealItem, 0, len(r.Ingredients))
	for _, ing := range r.Ingredients {
		food, ok := GetFood(ing.FoodID)
		if !ok {
			continue
		}
		items = append(items, toItem(food, ing.Grams*factor))
	}
	return items
}

type buildMealInput struct {
	Slot   Slot
	Kcal   float64
	Macros MacrosFloat
	Diet   DietProfile
	Role   string
	Seed   int
	// AllowRecipe falso força a composição por alimentos.
	AllowRecipe bool
}

// buildMeal monta uma refeição por função, não por percentagem: uma fonte de
// proteína, uma de hidratos, um vegetal — e gordura só se faltarem calorias.
func buildMeal(in buildMealInput) ([]MealItem, error) {
	base := AvailableFoodsInput{
		Style: in.Diet.Style, Budget: in.Diet.Budget,
		Exclusions: in.Diet.Exclusions, Slot: in.Slot,
	}
	light := in.Role == RoleLight || in.Slot == Snack || in.Slot == Breakfast

	// As refeições em torno do treino montam-se por macro, não por receita: é
	// aí que a composição tem de se deslocar.
	if in.Role == RoleBalanced && in.AllowRecipe {
		candidates, err := AvailableRecipes(AvailableRecipesInput{
			Style: in.Diet.Style, Slot: in.Slot, Exclusions: in.Diet.Exclusions,
		})
		if err != nil {
			return nil, err
		}
		if r, ok := bestRecipe(candidates, in.Kcal, in.Macros.Protein, in.Seed); ok {
			return normalize(fromRecipe(r, in.Kcal), in.Kcal), nil
		}
	}

	protein, ok, err := escolher(base, Protein, in.Seed)
	if err != nil {
		return nil, err
	}
	if !ok {
		protein, ok, err = escolher(base, Dairy, in.Seed)
		if err != nil {
			return nil, err
		}
	}
	temProtein := ok

	carb, temCarb, err := escolher(base, Carb, in.Seed+1)
	if err != nil {
		return nil, err
	}

	vegCategory := Vegetable
	if light {
		vegCategory = Fruit
	}
	veg, temVeg, err := escolher(base, vegCategory, in.Seed+2)
	if err != nil {
		return nil, err
	}

	items := []MealItem{}
	if temProtein {
		// A porção de proteína é dimensionada pelo alvo de proteína da refeição.
		perGram := protein.Macros.Protein / 100
		grams := protein.Serving
		if perGram > 0 {
			grams = math.Min(300, math.Max(40, in.Macros.Protein/perGram))
		}
		items = append(items, toItem(protein, grams))
	}
	if temVeg {
		items = append(items, toItem(veg, veg.Serving))
	}
	if temCarb {
		used := somaKcal(items)
		forCarb := math.Max(0, in.Kcal-used)
		grams := carb.Serving
		if carb.Kcal > 0 {
			grams = math.Min(400, math.Max(30, (forCarb*0.8*100)/carb.Kcal))
		}
		items = append(items, toItem(carb, grams))
	}

	// A gordura entra no fim, só para fechar as calorias que faltam.
	if gap := in.Kcal - somaKcal(items); gap > 60 {
		fat, temFat, err := escolher(base, Fat, in.Seed+3)
		if err != nil {
			return nil, err
		}
		if temFat && fat.Kcal > 0 {
			items = append(items, toItem(fat, math.Min(40, (gap*100)/fat.Kcal)))
		}
	}

	return normalize(items, in.Kcal), nil
}

func escolher(base AvailableFoodsInput, category FoodCategory, seed int) (Food, bool, error) {
	base.Category = category
	options, err := AvailableFoods(base)
	if err != nil {
		return Food{}, false, err
	}
	f, ok := pickOne(options, seed)
	return f, ok, nil
}

func somaKcal(items []MealItem) float64 {
	total := 0.0
	for _, i := range items {
		total += i.Kcal
	}
	return total
}

// normalize escala as porções para a refeição bater no seu alvo calórico.
//
// Sem isto, o que a pessoa lê no cartão não é o que o cartão diz somar.
func normalize(items []MealItem, targetKcal float64) []MealItem {
	total := somaKcal(items)
	if total <= 0 {
		return items
	}
	factor := targetKcal / total
	// Correcções minúsculas não valem a pena; distorções grandes também não são
	// comida a sério, por isso o factor é contido.
	if factor > 0.92 && factor < 1.08 {
		return items
	}
	bounded := math.Min(1.6, math.Max(0.5, factor))

	out := make([]MealItem, len(items))
	for i, item := range items {
		out[i] = MealItem{
			FoodID: item.FoodID,
			Name:   item.Name,
			Grams:  portable.RoundJS(item.Grams * bounded),
			Kcal:   portable.RoundJS(item.Kcal * bounded),
			Macros: MacrosFloat{
				Protein: portable.RoundTo(item.Macros.Protein*bounded, 1),
				Carbs:   portable.RoundTo(item.Macros.Carbs*bounded, 1),
				Fat:     portable.RoundTo(item.Macros.Fat*bounded, 1),
			},
		}
	}
	return out
}

// ScaleItemsTo reescala um conjunto de alimentos para outro alvo calórico.
//
// É o que permite aplicar uma refeição favorita a um lugar de tamanho
// diferente: o favorito guarda a combinação, não as gramas.
func ScaleItemsTo(items []MealItem, targetKcal float64) []MealItem {
	total := somaKcal(items)
	if total <= 0 || targetKcal <= 0 {
		return items
	}
	// Fora desta banda a porção deixava de ser comida a sério.
	factor := math.Min(2, math.Max(0.4, targetKcal/total))

	out := make([]MealItem, len(items))
	for i, item := range items {
		out[i] = MealItem{
			FoodID: item.FoodID,
			Name:   item.Name,
			Grams:  portable.RoundJS(item.Grams * factor),
			Kcal:   portable.RoundJS(item.Kcal * factor),
			Macros: MacrosFloat{
				Protein: portable.RoundTo(item.Macros.Protein*factor, 1),
				Carbs:   portable.RoundTo(item.Macros.Carbs*factor, 1),
				Fat:     portable.RoundTo(item.Macros.Fat*factor, 1),
			},
		}
	}
	return out
}

type RebuildMealInput struct {
	Meal PlannedMeal
	Diet DietProfile
	// Variant sobe a cada troca: tocar outra vez dá outra proposta em vez de
	// devolver a mesma.
	Variant int
}

// RebuildMeal remonta uma refeição com outra composição, mantendo intactos o
// alvo calórico e o de macros — trocar não pode desfazer as contas do dia.
//
// Porte de `rebuildMeal` em `meal-engine.ts`.
//
// ⚠️ O passo da semente tem de ser **1**. `pickOne` indexa com `seed % n`, e um
// passo maior fixa qualquer categoria cujo comprimento o divida: com passo 7 o
// vegetal nunca mudava, porque há exactamente 7 vegetais.
//
// O segundo eixo é a receita. Uma refeição "equilibrada" nasce de uma receita, e
// às vezes só há uma aceitável para aquele alvo — aí a semente não muda nada e a
// troca não fazia rigorosamente nada. Quando isso acontece monta-se por
// alimentos, que é o que dá alternativa de facto.
func RebuildMeal(c Config, in RebuildMealInput) (PlannedMeal, error) {
	base := seedFromDay(in.Meal.ID)
	atual := assinatura(in.Meal.Items)

	for step := 0; step < 10; step++ {
		for _, comReceita := range []bool{true, false} {
			items, err := buildMeal(buildMealInput{
				Slot: in.Meal.Slot, Kcal: float64(in.Meal.Kcal), Macros: in.Meal.Macros,
				Diet: in.Diet, Role: in.Meal.Role,
				Seed: base + in.Variant + step, AllowRecipe: comReceita,
			})
			if err != nil {
				return PlannedMeal{}, err
			}
			if assinatura(items) != atual {
				out := in.Meal
				out.Items = items
				return out, nil
			}
		}
	}
	// Sem alternativa possível (dieta muito restrita), devolve-se o que havia.
	// Não é erro: é a resposta honesta de "não há outra coisa que sirva".
	return in.Meal, nil
}

func assinatura(items []MealItem) string {
	ids := make([]string, 0, len(items))
	for _, i := range items {
		ids = append(ids, i.FoodID)
	}
	return strings.Join(ids, "+")
}

/*
 * OpcoesDeRefeicao devolve alternativas para um lugar da refeição.
 *
 * ⚠️ O que existia era **uma** refeição por lugar e um botão "trocar" que dava
 * a seguinte. Quem não gostasse da primeira tocava até acertar, sem saber
 * quantas havia nem o que vinha a seguir — e sem forma de voltar à que tinha
 * visto duas trocas atrás. A especificação pede o contrário: mostrar as opções
 * e deixar escolher.
 *
 * A primeira é sempre a que está no plano: as alternativas são alternativas
 * **a alguma coisa**, e tirar a actual da lista fazia o ecrã propor uma troca
 * onde a pessoa só queria ver o que havia.
 *
 * Devolve menos do que se pede quando não há mais nada que sirva. Uma dieta
 * restrita com um alvo apertado tem mesmo poucas respostas, e inventar
 * repetições para encher três cartões era mentir com a interface.
 */
func OpcoesDeRefeicao(c Config, in RebuildMealInput, quantas int) ([]PlannedMeal, error) {
	if quantas <= 0 {
		return nil, nil
	}
	out := []PlannedMeal{in.Meal}
	vistas := map[string]bool{assinatura(in.Meal.Items): true}

	// O mesmo passo de 1 do `RebuildMeal`, e pela mesma razão: ver o comentário
	// lá. Vinte tentativas porque cada variante pode cair numa já vista.
	base := seedFromDay(in.Meal.ID)
	for step := 0; step < 20 && len(out) < quantas; step++ {
		for _, comReceita := range []bool{true, false} {
			items, err := buildMeal(buildMealInput{
				Slot: in.Meal.Slot, Kcal: float64(in.Meal.Kcal), Macros: in.Meal.Macros,
				Diet: in.Diet, Role: in.Meal.Role,
				Seed: base + in.Variant + step, AllowRecipe: comReceita,
			})
			if err != nil {
				return nil, err
			}
			chave := assinatura(items)
			if vistas[chave] {
				continue
			}
			vistas[chave] = true
			alternativa := in.Meal
			alternativa.Items = items
			out = append(out, alternativa)
			if len(out) >= quantas {
				break
			}
		}
	}
	return out, nil
}
