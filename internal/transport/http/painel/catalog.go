package painel

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/portable"
	"github.com/airosp/airo-api/internal/engine/training"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

// reload entrega o catálogo gravado ao motor. Uma falha não desfaz a gravação
// — o motor continua com o catálogo anterior, que é coerente — mas diz-se.
func (a *API) reload(ctx context.Context) {
	if a.ReloadCatalog == nil {
		return
	}
	if err := a.ReloadCatalog(context.WithoutCancel(ctx)); err != nil && a.Log != nil {
		a.Log.Error("recarregar o catálogo no motor", "error", err)
	}
}

// ── Exercícios ───────────────────────────────────────────────────────────────

var patterns = []string{"push", "pull", "squat", "hinge", "lunge", "core", "cardio", "mobility"}

func viewExercise(e repo.ExerciseRow, tpl, people map[string]int) map[string]any {
	ps := []string{}
	for _, p := range training.PadroesDe(e.Exercise) {
		ps = append(ps, string(p))
	}
	return map[string]any{
		"id": e.ID, "name": e.Name, "pattern": e.Pattern, "patterns": ps, "muscles": listOr(e.Muscles),
		"equipment": listOr(e.Equipment), "level": e.Level, "impact": training.ImpactoDe(e.Exercise),
		"measure": e.Measure, "cue": e.Cue, "instructions": listOr(e.Instructions),
		"contraindications": listOr(e.Contraindications), "demoUrl": e.DemoURL, "demoQuery": e.DemoQuery,
		"warmup": e.Warmup, "cooldown": e.Cooldown, "active": e.Active,
		"usage": map[string]int{"templates": tpl[e.ID], "people": people[e.ID]},
	}
}

func listOr(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (a *API) ExercisesList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Catalog.Exercises(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os exercícios.")
		return
	}
	tpl, people, err := a.Catalog.ExerciseUsage(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os exercícios.")
		return
	}
	musc, equip := map[string]bool{}, map[string]bool{}
	out := make([]map[string]any, 0, len(l))
	for _, e := range l {
		out = append(out, viewExercise(e, tpl, people))
		for _, m := range e.Muscles {
			musc[m] = true
		}
		for _, q := range e.Equipment {
			equip[q] = true
		}
	}
	ok(w, map[string]any{"items": out, "options": map[string]any{
		"patterns": patterns, "muscles": keys(musc), "equipment": keys(equip),
		"levels": []string{"beginner", "intermediate", "advanced"}, "impacts": []string{"low", "moderate", "high"},
		"measures": []string{"reps", "time"},
	}})
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *API) saveExercise(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in struct {
		ID                string   `json:"id"`
		Name              string   `json:"name"`
		Pattern           string   `json:"pattern"`
		Patterns          []string `json:"patterns"`
		Muscles           []string `json:"muscles"`
		Equipment         []string `json:"equipment"`
		Level             string   `json:"level"`
		Impact            string   `json:"impact"`
		Measure           string   `json:"measure"`
		Cue               string   `json:"cue"`
		Instructions      []string `json:"instructions"`
		Contraindications []string `json:"contraindications"`
		DemoURL           string   `json:"demoUrl"`
		DemoQuery         string   `json:"demoQuery"`
		Warmup            bool     `json:"warmup"`
		Cooldown          bool     `json:"cooldown"`
		Active            *bool    `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		invalid(w, "O nome é obrigatório.", "name")
		return
	case !oneOf(in.Pattern, patterns...):
		invalid(w, "Padrão de movimento desconhecido.", "pattern")
		return
	case !oneOf(in.Level, "beginner", "intermediate", "advanced"):
		invalid(w, "Nível desconhecido.", "level")
		return
	case !oneOf(in.Impact, "low", "moderate", "high"):
		invalid(w, "Impacto desconhecido.", "impact")
		return
	case !oneOf(in.Measure, "reps", "time"):
		invalid(w, "Medida desconhecida.", "measure")
		return
	case strings.TrimSpace(in.Cue) == "":
		invalid(w, "A dica de execução é obrigatória — é o que a pessoa lê a meio da série.", "cue")
		return
	case len(clean(in.Muscles)) == 0:
		invalid(w, "Indica pelo menos um músculo.", "muscles")
		return
	}
	if create {
		id = slugOf(in.ID)
		if id == "" {
			id = slugOf(in.Name)
		}
	}
	e := repo.ExerciseRow{Instructions: clean(in.Instructions), Contraindications: clean(in.Contraindications), Active: true}
	if in.Active != nil {
		e.Active = *in.Active
	}
	e.ID, e.Name, e.Pattern = id, in.Name, training.MovementPattern(in.Pattern)
	e.Muscles, e.Equipment, e.Level = clean(in.Muscles), clean(in.Equipment), training.Experience(in.Level)
	e.Measure, e.Cue = training.Measure(in.Measure), strings.TrimSpace(in.Cue)
	e.DemoURL, e.DemoQuery, e.Warmup, e.Cooldown = strings.TrimSpace(in.DemoURL), strings.TrimSpace(in.DemoQuery), in.Warmup, in.Cooldown
	if in.Impact != "low" {
		e.Impact = training.Impact(in.Impact)
	}
	ps := clean(in.Patterns)
	if !(len(ps) == 0 || (len(ps) == 1 && ps[0] == in.Pattern)) {
		// O principal vai sempre à frente: é por ele que o motor prescreve.
		e.Patterns = []training.MovementPattern{e.Pattern}
		for _, p := range ps {
			if p != in.Pattern && oneOf(p, patterns...) {
				e.Patterns = append(e.Patterns, training.MovementPattern(p))
			}
		}
		if len(e.Patterns) == 1 {
			e.Patterns = nil
		}
	}
	if err := a.Catalog.SaveExercise(r.Context(), e, create); err != nil {
		writeRepoErr(w, r, err, "o exercício")
		return
	}
	a.reload(r.Context())
	a.audit(r, "exercise_saved", in.Name, "")
	saved, err := a.Catalog.Exercise(r.Context(), id)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Gravado, mas não foi possível ler de volta.")
		return
	}
	tpl, people, _ := a.Catalog.ExerciseUsage(r.Context())
	if create {
		created(w, viewExercise(saved, tpl, people))
		return
	}
	ok(w, viewExercise(saved, tpl, people))
}

func (a *API) ExerciseCreate(w http.ResponseWriter, r *http.Request) { a.saveExercise(w, r, "", true) }
func (a *API) ExerciseUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveExercise(w, r, r.PathValue("id"), false)
}

// ── Alimentos ────────────────────────────────────────────────────────────────

func viewFood(f repo.FoodRow) map[string]any {
	styles := []string{}
	for _, s := range f.Styles {
		styles = append(styles, string(s))
	}
	good := []string{}
	for _, s := range f.GoodFor {
		good = append(good, string(s))
	}
	return map[string]any{
		"id": f.ID, "name": f.Name, "category": f.Category, "kcal": f.Kcal, "protein": f.Macros.Protein,
		"carbs": f.Macros.Carbs, "fat": f.Macros.Fat, "fiber": f.Fiber, "serving": f.Serving,
		"styles": styles, "budget": f.Budget, "goodFor": good, "region": f.Region, "active": f.Active,
	}
}

func (a *API) FoodsList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Catalog.Foods(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os alimentos.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, f := range l {
		out = append(out, viewFood(f))
	}
	ok(w, map[string]any{"items": out})
}

var styleNames = map[string]string{"omnivore": "omnívoro", "high_protein": "rico em proteína", "vegetarian": "vegetariano", "vegan": "vegano"}

func (a *API) saveFood(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Category string   `json:"category"`
		Kcal     float64  `json:"kcal"`
		Protein  float64  `json:"protein"`
		Carbs    float64  `json:"carbs"`
		Fat      float64  `json:"fat"`
		Fiber    *float64 `json:"fiber"`
		Serving  float64  `json:"serving"`
		Styles   []string `json:"styles"`
		Budget   string   `json:"budget"`
		GoodFor  []string `json:"goodFor"`
		Region   string   `json:"region"`
		Active   *bool    `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		invalid(w, "O nome é obrigatório.", "name")
		return
	case !oneOf(in.Category, "protein", "carb", "vegetable", "fat", "fruit", "dairy"):
		invalid(w, "Categoria desconhecida.", "category")
		return
	case !oneOf(in.Budget, "low", "medium", "high"):
		invalid(w, "Custo desconhecido.", "budget")
		return
	case in.Kcal < 0 || in.Kcal > 900:
		invalid(w, "Calorias por 100 g fora do intervalo (0–900).", "kcal")
		return
	case in.Protein < 0 || in.Carbs < 0 || in.Fat < 0 || in.Protein+in.Carbs+in.Fat > 100:
		invalid(w, "Os macros por 100 g não podem somar mais de 100 g.", "protein")
		return
	case in.Serving < 1 || in.Serving > 2000:
		invalid(w, "A porção habitual tem de estar entre 1 e 2000 g.", "serving")
		return
	}
	// Coerência: as calorias têm de bater com os macros (4/4/9), com folga para
	// fibra e álcool. Um erro de digitação aqui muda o plano de toda a gente.
	calc := in.Protein*4 + in.Carbs*4 + in.Fat*9
	if calc > 0 && (in.Kcal < calc*0.75 || in.Kcal > calc*1.25+20) {
		invalid(w, "As calorias não batem com os macros (deviam rondar "+portableKcal(calc)+" kcal).", "kcal")
		return
	}
	styles := []nutrition.DietStyle{}
	for _, s := range clean(in.Styles) {
		if !oneOf(s, "omnivore", "high_protein", "vegetarian", "vegan") {
			invalid(w, "Estilo desconhecido: "+s, "styles")
			return
		}
		styles = append(styles, nutrition.DietStyle(s))
	}
	good := []nutrition.Slot{}
	for _, s := range clean(in.GoodFor) {
		if !oneOf(s, "breakfast", "lunch", "snack", "dinner", "supper") {
			invalid(w, "Refeição desconhecida: "+s, "goodFor")
			return
		}
		good = append(good, nutrition.Slot(s))
	}
	if create {
		id = slugOf(in.ID)
		if id == "" {
			id = slugOf(in.Name)
		}
	}
	f := repo.FoodRow{Fiber: in.Fiber, Active: true}
	if in.Active != nil {
		f.Active = *in.Active
	}
	f.ID, f.Name, f.Category, f.Kcal = id, in.Name, nutrition.FoodCategory(in.Category), round1(in.Kcal)
	f.Macros = nutrition.MacrosFloat{Protein: round1(in.Protein), Carbs: round1(in.Carbs), Fat: round1(in.Fat)}
	f.Serving, f.Styles, f.Budget, f.GoodFor, f.Region = float64(int(in.Serving)), styles, nutrition.Budget(in.Budget), good, strings.TrimSpace(in.Region)
	if err := a.Catalog.SaveFood(r.Context(), f, create); err != nil {
		writeRepoErr(w, r, err, "o alimento")
		return
	}
	a.reload(r.Context())
	a.audit(r, "food_saved", in.Name, "")
	saved, err := a.Catalog.Food(r.Context(), id)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Gravado, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, viewFood(saved))
		return
	}
	ok(w, viewFood(saved))
}

func round1(v float64) float64 { return portable.RoundTo(v, 1) }

func portableKcal(v float64) string { return strconv.Itoa(int(portable.RoundJS(v))) }

func (a *API) FoodCreate(w http.ResponseWriter, r *http.Request) { a.saveFood(w, r, "", true) }
func (a *API) FoodUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveFood(w, r, r.PathValue("id"), false)
}

// ── Receitas ─────────────────────────────────────────────────────────────────

type ingredientIn struct {
	FoodID string  `json:"foodId"`
	Grams  float64 `json:"grams"`
}

// computeRecipe soma os ingredientes com a base gravada — a mesma conta de
// `nutrition.ComputeRecipeNutrition`, e os estilos que o prato pode ter: um
// prato só é vegano se todos os ingredientes o forem.
func computeRecipe(foods map[string]repo.FoodRow, ings []ingredientIn, serves int) (map[string]any, []map[string]any, error) {
	if serves < 1 {
		serves = 1
	}
	var kcal, p, c, f float64
	lines := []map[string]any{}
	allowed := map[string]bool{"omnivore": true, "high_protein": true, "vegetarian": true, "vegan": true}
	blocked := []map[string]any{}
	seenBlock := map[string]bool{}
	for _, ing := range ings {
		food, ok := foods[ing.FoodID]
		if !ok {
			return nil, nil, errUnknownFood(ing.FoodID)
		}
		k := ing.Grams / 100
		lk, lp, lc, lf := food.Kcal*k, food.Macros.Protein*k, food.Macros.Carbs*k, food.Macros.Fat*k
		kcal, p, c, f = kcal+lk, p+lp, c+lc, f+lf
		lines = append(lines, map[string]any{
			"foodId": food.ID, "name": food.Name, "grams": ing.Grams, "kcal": portable.RoundJS(lk),
			"protein": round1(lp), "carbs": round1(lc), "fat": round1(lf),
		})
		has := map[string]bool{}
		for _, s := range food.Styles {
			has[string(s)] = true
		}
		for s := range allowed {
			if !has[s] && allowed[s] {
				allowed[s] = false
				if !seenBlock[s] {
					seenBlock[s] = true
					blocked = append(blocked, map[string]any{"style": s, "reason": food.Name + " não é " + styleNames[s]})
				}
			}
		}
	}
	al := []string{}
	for _, s := range []string{"omnivore", "high_protein", "vegetarian", "vegan"} {
		if allowed[s] && len(ings) > 0 {
			al = append(al, s)
		}
	}
	n := float64(serves)
	calc := map[string]any{
		"nutrition":     map[string]any{"kcal": portable.RoundJS(kcal), "protein": round1(p), "carbs": round1(c), "fat": round1(f)},
		"perServing":    map[string]any{"kcal": portable.RoundJS(kcal / n), "protein": round1(p / n), "carbs": round1(c / n), "fat": round1(f / n)},
		"allowedStyles": al,
		"blockedStyles": blocked,
	}
	return calc, lines, nil
}

type errUnknownFood string

func (e errUnknownFood) Error() string { return "alimento desconhecido: " + string(e) }

func (a *API) foodIndex(ctx context.Context) (map[string]repo.FoodRow, error) {
	l, err := a.Catalog.Foods(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]repo.FoodRow, len(l))
	for _, f := range l {
		m[f.ID] = f
	}
	return m, nil
}

func (a *API) viewRecipe(rec repo.RecipeRow, foods map[string]repo.FoodRow) map[string]any {
	ings := []ingredientIn{}
	for _, i := range rec.Ingredients {
		ings = append(ings, ingredientIn{FoodID: i.FoodID, Grams: i.Grams})
	}
	calc, lines, err := computeRecipe(foods, ings, rec.Serves)
	if err != nil {
		calc, lines = map[string]any{}, []map[string]any{}
	}
	slots, styles := []string{}, []string{}
	for _, s := range rec.Slots {
		slots = append(slots, string(s))
	}
	for _, s := range rec.Styles {
		styles = append(styles, string(s))
	}
	out := map[string]any{
		"id": rec.ID, "name": rec.Name, "slots": slots, "styles": styles, "serves": rec.Serves,
		"active": rec.Active, "validatedBy": rec.ValidatedBy, "validatedAt": iso(rec.ValidatedAt),
		"ingredients": lines,
	}
	for k, v := range calc {
		out[k] = v
	}
	return out
}

func (a *API) RecipesList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Catalog.Recipes(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os pratos.")
		return
	}
	foods, err := a.foodIndex(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os pratos.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, rec := range l {
		out = append(out, a.viewRecipe(rec, foods))
	}
	ok(w, map[string]any{"items": out})
}

func (a *API) RecipePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ingredients []ingredientIn `json:"ingredients"`
		Serves      int            `json:"serves"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	foods, err := a.foodIndex(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível calcular.")
		return
	}
	calc, lines, err := computeRecipe(foods, in.Ingredients, in.Serves)
	if err != nil {
		invalid(w, err.Error(), "ingredients")
		return
	}
	calc["ingredients"] = lines
	ok(w, calc)
}

func (a *API) saveRecipe(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in struct {
		ID          string         `json:"id"`
		Name        string         `json:"name"`
		Slots       []string       `json:"slots"`
		Styles      []string       `json:"styles"`
		Serves      int            `json:"serves"`
		Active      *bool          `json:"active"`
		Ingredients []ingredientIn `json:"ingredients"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		invalid(w, "O nome é obrigatório.", "name")
		return
	}
	if in.Serves < 1 || in.Serves > 20 {
		invalid(w, "As porções têm de estar entre 1 e 20.", "serves")
		return
	}
	if len(in.Ingredients) == 0 {
		invalid(w, "Um prato precisa de ingredientes.", "ingredients")
		return
	}
	for _, i := range in.Ingredients {
		if i.Grams < 1 || i.Grams > 2000 {
			invalid(w, "As gramas de cada ingrediente têm de estar entre 1 e 2000.", "ingredients")
			return
		}
	}
	slots := []nutrition.Slot{}
	for _, s := range clean(in.Slots) {
		if !oneOf(s, "breakfast", "lunch", "snack", "dinner", "supper") {
			invalid(w, "Refeição desconhecida: "+s, "slots")
			return
		}
		slots = append(slots, nutrition.Slot(s))
	}
	if len(slots) == 0 {
		invalid(w, "Escolhe pelo menos uma refeição.", "slots")
		return
	}
	foods, err := a.foodIndex(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o prato.")
		return
	}
	calc, _, err := computeRecipe(foods, in.Ingredients, in.Serves)
	if err != nil {
		invalid(w, err.Error(), "ingredients")
		return
	}
	allowed := map[string]bool{}
	for _, s := range calc["allowedStyles"].([]string) {
		allowed[s] = true
	}
	styles := []nutrition.DietStyle{}
	for _, s := range clean(in.Styles) {
		if !allowed[s] {
			// Um prato com frango marcado como vegetariano chega a quem não
			// come carne. Recusa-se no servidor, não só no ecrã.
			reason := "não é compatível com os ingredientes"
			for _, b := range calc["blockedStyles"].([]map[string]any) {
				if b["style"] == s {
					reason = b["reason"].(string)
				}
			}
			invalid(w, "Não pode ser "+styleNames[s]+": "+reason+".", "styles")
			return
		}
		styles = append(styles, nutrition.DietStyle(s))
	}
	if create {
		id = slugOf(in.ID)
		if id == "" {
			id = slugOf(in.Name)
		}
	}
	rec := repo.RecipeRow{Active: true}
	if in.Active != nil {
		rec.Active = *in.Active
	}
	rec.ID, rec.Name, rec.Slots, rec.Styles, rec.Serves = id, in.Name, slots, styles, in.Serves
	for _, i := range in.Ingredients {
		rec.Ingredients = append(rec.Ingredients, nutrition.RecipeIngredient{FoodID: i.FoodID, Grams: float64(int(i.Grams))})
	}
	if err := a.Catalog.SaveRecipe(r.Context(), rec, create); err != nil {
		writeRepoErr(w, r, err, "o prato")
		return
	}
	a.reload(r.Context())
	a.audit(r, "recipe_saved", in.Name, "Valores recalculados pelo motor")
	saved, err := a.Catalog.Recipe(r.Context(), id)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Gravado, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, a.viewRecipe(saved, foods))
		return
	}
	ok(w, a.viewRecipe(saved, foods))
}

func (a *API) RecipeCreate(w http.ResponseWriter, r *http.Request) { a.saveRecipe(w, r, "", true) }
func (a *API) RecipeUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveRecipe(w, r, r.PathValue("id"), false)
}

func (a *API) RecipeValidate(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	id := r.PathValue("id")
	if err := a.Catalog.ValidateRecipe(r.Context(), id, m.Name); err != nil {
		writeRepoErr(w, r, err, "o prato")
		return
	}
	a.audit(r, "recipe_validated", id, "")
	foods, _ := a.foodIndex(r.Context())
	saved, err := a.Catalog.Recipe(r.Context(), id)
	if err != nil {
		writeRepoErr(w, r, err, "o prato")
		return
	}
	ok(w, a.viewRecipe(saved, foods))
}
