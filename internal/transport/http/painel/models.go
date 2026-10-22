package painel

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/engine/portable"
	"github.com/airosp/airo-api/internal/engine/training"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

// ── Modelos de treino ────────────────────────────────────────────────────────

type templateItem struct {
	ExerciseID  string `json:"exerciseId"`
	Sets        int    `json:"sets"`
	Reps        int    `json:"reps"`
	Seconds     int    `json:"seconds"`
	RestSeconds int    `json:"restSeconds"`
	RPE         int    `json:"rpe"`
}

type templateBlocks struct {
	Warmup   []templateItem `json:"warmup"`
	Main     []templateItem `json:"main"`
	Cooldown []templateItem `json:"cooldown"`
}

var equipNames = map[string]string{
	"bands": "elásticos", "barbell": "barra", "cardio": "máquina de cardio", "dumbbells": "halteres",
	"kettlebell": "kettlebell", "machines": "máquinas",
}

// templateChecks é a verificação automática, com os critérios do motor: a
// duração estimada, o impacto para o nível, o material e a estrutura.
func templateChecks(b templateBlocks, level string, minutes int) ([]map[string]any, int) {
	checks := []map[string]any{}
	add := func(key string, okv bool, label string) {
		checks = append(checks, map[string]any{"key": key, "ok": okv, "label": label})
	}
	secs := 0
	high, unknown := []string{}, []string{}
	equip := map[string]bool{}
	for _, list := range [][]templateItem{b.Warmup, b.Main, b.Cooldown} {
		for _, it := range list {
			e, found := training.GetExercise(it.ExerciseID)
			if !found {
				unknown = append(unknown, it.ExerciseID)
				continue
			}
			work := it.Seconds
			if e.Measure != training.Time {
				// Três segundos por repetição: o ritmo que o motor assume.
				work = it.Reps * 3
			}
			sets := max(it.Sets, 1)
			secs += sets*work + (sets-1)*it.RestSeconds
			if training.ImpactoDe(e) == training.ImpactoAlto {
				high = append(high, e.Name)
			}
			for _, q := range e.Equipment {
				equip[q] = true
			}
		}
	}
	est := int(math.Round(float64(secs) / 60))
	diff := math.Abs(float64(est-minutes)) / math.Max(float64(minutes), 1)
	add("duration", diff <= 0.2, fmt.Sprintf("Duração estimada %d min para %d min previstos", est, minutes))
	if level == "beginner" && len(high) > 0 {
		add("impact", false, "Impacto alto num treino de iniciação: "+strings.Join(high, ", "))
	} else {
		add("impact", true, "Impacto adequado ao nível")
	}
	if len(equip) == 0 {
		add("equipment", true, "Só o corpo — serve a toda a gente")
	} else {
		names := []string{}
		for _, k := range keys(equip) {
			n := equipNames[k]
			if n == "" {
				n = k
			}
			names = append(names, n)
		}
		add("equipment", true, "Precisa de "+strings.Join(names, ", ")+" — só aparece a quem os tem")
	}
	add("structure", len(b.Warmup) > 0 && len(b.Main) > 0 && len(b.Cooldown) > 0,
		"Aquecimento, parte principal e retorno à calma")
	if len(unknown) > 0 {
		add("catalog", false, "Exercícios que já não estão no catálogo: "+strings.Join(unknown, ", "))
	}
	return checks, est
}

func viewTemplate(t repo.TemplateRow) map[string]any {
	var b templateBlocks
	_ = json.Unmarshal(t.Blocks, &b)
	for _, l := range []*[]templateItem{&b.Warmup, &b.Main, &b.Cooldown} {
		if *l == nil {
			*l = []templateItem{}
		}
	}
	checks, est := templateChecks(b, t.Level, t.Minutes)
	return map[string]any{
		"id": t.ID, "name": t.Name, "focus": t.Focus, "level": t.Level, "minutes": t.Minutes, "goal": t.Goal,
		"status": t.Status, "blocks": b, "checks": checks, "estimatedMinutes": est,
		"updatedAt": t.UpdatedAt.UTC().Format(time.RFC3339), "updatedBy": t.UpdatedBy,
	}
}

func (a *API) TemplatesList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Models.Templates(r.Context(), "")
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os modelos de treino.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, t := range l {
		out = append(out, viewTemplate(t))
	}
	ok(w, map[string]any{"items": out})
}

func (a *API) saveTemplate(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in struct {
		Name    string         `json:"name"`
		Focus   string         `json:"focus"`
		Level   string         `json:"level"`
		Minutes int            `json:"minutes"`
		Goal    string         `json:"goal"`
		Status  string         `json:"status"`
		Blocks  templateBlocks `json:"blocks"`
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
	case !oneOf(in.Focus, "upper", "lower", "cardio", "full", "mobility"):
		invalid(w, "Foco desconhecido.", "focus")
		return
	case !oneOf(in.Level, "beginner", "intermediate", "advanced"):
		invalid(w, "Nível desconhecido.", "level")
		return
	case in.Minutes < 5 || in.Minutes > 180:
		invalid(w, "A duração tem de estar entre 5 e 180 minutos.", "minutes")
		return
	}
	if in.Status == "" {
		in.Status = "draft"
	}
	if !oneOf(in.Status, "draft", "approved") {
		invalid(w, "Estado desconhecido.", "status")
		return
	}
	for _, list := range [][]templateItem{in.Blocks.Warmup, in.Blocks.Main, in.Blocks.Cooldown} {
		for _, it := range list {
			if _, found := training.GetExercise(it.ExerciseID); !found {
				invalid(w, "Exercício desconhecido: "+it.ExerciseID, "blocks")
				return
			}
			if it.Sets < 1 || it.Sets > 10 || it.Reps < 0 || it.Reps > 100 || it.Seconds < 0 || it.Seconds > 3600 ||
				it.RestSeconds < 0 || it.RestSeconds > 600 || it.RPE < 0 || it.RPE > 10 {
				invalid(w, "Séries, repetições, descanso ou RPE fora do intervalo.", "blocks")
				return
			}
		}
	}
	// Aprovar exige a verificação toda verde: um modelo aprovado é o que o
	// motor pode vir a servir.
	if in.Status == "approved" {
		checks, _ := templateChecks(in.Blocks, in.Level, in.Minutes)
		for _, c := range checks {
			if c["ok"] == false {
				invalid(w, "Não se aprova com verificações por resolver: "+c["label"].(string)+".", "status")
				return
			}
		}
	}
	if create {
		id = slugOf(in.Name)
		if id == "" {
			invalid(w, "O nome precisa de letras.", "name")
			return
		}
	}
	m, _ := memberOf(r.Context())
	blocks, _ := json.Marshal(in.Blocks)
	t := repo.TemplateRow{ID: id, Name: in.Name, Focus: in.Focus, Level: in.Level, Minutes: in.Minutes,
		Goal: strings.TrimSpace(in.Goal), Status: in.Status, Blocks: blocks, UpdatedBy: m.Name}
	if err := a.Models.SaveTemplate(r.Context(), t, create); err != nil {
		writeRepoErr(w, r, err, "o modelo")
		return
	}
	a.audit(r, "template_saved", in.Name, "")
	l, err := a.Models.Templates(r.Context(), id)
	if err != nil || len(l) == 0 {
		apierr.WriteInternal(w, r, err, "Gravado, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, viewTemplate(l[0]))
		return
	}
	ok(w, viewTemplate(l[0]))
}

func (a *API) TemplateCreate(w http.ResponseWriter, r *http.Request) { a.saveTemplate(w, r, "", true) }
func (a *API) TemplateUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveTemplate(w, r, r.PathValue("id"), false)
}

func (a *API) TemplateDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Models.DeleteTemplate(r.Context(), id); err != nil {
		writeRepoErr(w, r, err, "o modelo")
		return
	}
	a.audit(r, "template_deleted", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// ── Modelos de dieta ─────────────────────────────────────────────────────────

type dietRules struct {
	DeficitPct   float64 `json:"deficitPct"`
	MinKcal      int     `json:"minKcal"`
	ProteinPerKg float64 `json:"proteinPerKg"`
	FatPct       float64 `json:"fatPct"`
	// O exemplo com que o ecrã mostra as regras a funcionar.
	ExampleWeightKg float64 `json:"exampleWeightKg"`
	ExampleTDEE     int     `json:"exampleTdee"`
}

type dietMeal struct {
	Slot     string `json:"slot"`
	RecipeID string `json:"recipeId"`
}

type dietDay struct {
	Day   int        `json:"day"`
	Meals []dietMeal `json:"meals"`
}

func (d *dietRules) defaults() {
	if d.MinKcal == 0 {
		d.MinKcal = 1500
	}
	if d.ExampleWeightKg == 0 {
		d.ExampleWeightKg = 75
	}
	if d.ExampleTDEE == 0 {
		d.ExampleTDEE = 2400
	}
}

// example aplica as regras a uma pessoa de exemplo. As contas são as da
// estratégia: alvo, depois proteína por quilo, gordura em percentagem, e os
// hidratos ficam com o resto.
func (d dietRules) example() map[string]any {
	kcal := float64(d.ExampleTDEE) * (1 - d.DeficitPct/100)
	kcal = math.Max(kcal, float64(d.MinKcal))
	p := d.ExampleWeightKg * d.ProteinPerKg
	f := kcal * d.FatPct / 100 / 9
	c := math.Max(0, (kcal-p*4-f*9)/4)
	return map[string]any{
		"weightKg": d.ExampleWeightKg, "tdee": d.ExampleTDEE, "kcal": int(portable.RoundJS(kcal)),
		"protein": int(math.Round(p)), "carbs": int(math.Round(c)), "fat": int(math.Round(f)),
	}
}

func (a *API) viewDiet(d repo.DietModelRow, recipes map[string]map[string]any) map[string]any {
	var rules dietRules
	_ = json.Unmarshal(d.Rules, &rules)
	rules.defaults()
	ex := rules.example()
	target := float64(ex["kcal"].(int))
	var days []dietDay
	_ = json.Unmarshal(d.Week, &days)
	week := []map[string]any{}
	for _, dd := range days {
		meals := []map[string]any{}
		var k, p, c, f float64
		for _, m := range dd.Meals {
			rec := recipes[m.RecipeID]
			var kcal any
			name := ""
			if rec != nil {
				per := rec["perServing"].(map[string]any)
				kcal = per["kcal"]
				name, _ = rec["name"].(string)
				k += per["kcal"].(float64)
				p += per["protein"].(float64)
				c += per["carbs"].(float64)
				f += per["fat"].(float64)
			}
			meals = append(meals, map[string]any{"slot": m.Slot, "recipeId": m.RecipeID, "recipeName": name, "kcal": kcal})
		}
		week = append(week, map[string]any{
			"day": dd.Day, "meals": meals,
			"totals":   map[string]any{"kcal": portable.RoundJS(k), "protein": round1(p), "carbs": round1(c), "fat": round1(f)},
			"onTarget": target > 0 && math.Abs(k-target)/target <= 0.05,
		})
	}
	var comments any
	_ = json.Unmarshal(d.Comments, &comments)
	if comments == nil {
		comments = []any{}
	}
	return map[string]any{
		"id": d.ID, "name": d.Name, "goal": d.Goal, "mealsPerDay": d.MealsPerDay, "status": d.Status,
		"rules": rules, "example": ex, "week": week, "comments": comments,
		"updatedAt": d.UpdatedAt.UTC().Format(time.RFC3339), "updatedBy": d.UpdatedBy, "approvedBy": d.ApprovedBy,
	}
}

func (a *API) recipeIndex(r *http.Request) (map[string]map[string]any, error) {
	l, err := a.Catalog.Recipes(r.Context())
	if err != nil {
		return nil, err
	}
	foods, err := a.foodIndex(r.Context())
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, rec := range l {
		v := a.viewRecipe(rec, foods)
		// Os números vêm do motor como float64 ou int; normaliza-se para as
		// somas do dia.
		if per, ok := v["perServing"].(map[string]any); ok {
			for k, x := range per {
				switch n := x.(type) {
				case int:
					per[k] = float64(n)
				case float64:
					per[k] = n
				}
			}
			out[rec.ID] = v
		}
	}
	return out, nil
}

func (a *API) DietsList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Models.DietModels(r.Context(), "")
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os modelos de dieta.")
		return
	}
	recs, err := a.recipeIndex(r)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os modelos de dieta.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, d := range l {
		out = append(out, a.viewDiet(d, recs))
	}
	ok(w, map[string]any{"items": out})
}

func (a *API) saveDiet(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in struct {
		Name        string    `json:"name"`
		Goal        string    `json:"goal"`
		MealsPerDay int       `json:"mealsPerDay"`
		Rules       dietRules `json:"rules"`
		Week        []dietDay `json:"week"`
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
	case !oneOf(in.Goal, "lose_fat", "gain_muscle", "maintain", "performance", "health"):
		invalid(w, "Objetivo desconhecido.", "goal")
		return
	case in.MealsPerDay < 3 || in.MealsPerDay > 5:
		invalid(w, "Entre 3 e 5 refeições por dia.", "mealsPerDay")
		return
	case in.Rules.DeficitPct < -25 || in.Rules.DeficitPct > 25:
		invalid(w, "O défice tem de estar entre −25% e 25%.", "rules.deficitPct")
		return
	case in.Rules.MinKcal != 0 && in.Rules.MinKcal < 1500:
		// O mesmo piso que a base de dados obriga em `nutrition_strategy`.
		invalid(w, "O mínimo não pode ficar abaixo de 1 500 kcal.", "rules.minKcal")
		return
	case in.Rules.ProteinPerKg < 0.8 || in.Rules.ProteinPerKg > 3:
		invalid(w, "A proteína tem de estar entre 0,8 e 3 g/kg.", "rules.proteinPerKg")
		return
	case in.Rules.FatPct < 15 || in.Rules.FatPct > 45:
		invalid(w, "A gordura tem de estar entre 15% e 45%.", "rules.fatPct")
		return
	}
	in.Rules.defaults()
	recs, err := a.recipeIndex(r)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar.")
		return
	}
	if in.Week == nil {
		in.Week = []dietDay{}
	}
	for _, d := range in.Week {
		if d.Day < 0 || d.Day > 6 {
			invalid(w, "Dia da semana inválido.", "week")
			return
		}
		for _, m := range d.Meals {
			if !oneOf(m.Slot, "breakfast", "lunch", "snack", "dinner", "supper") {
				invalid(w, "Refeição desconhecida: "+m.Slot, "week")
				return
			}
			if m.RecipeID != "" && recs[m.RecipeID] == nil {
				invalid(w, "Prato desconhecido: "+m.RecipeID, "week")
				return
			}
		}
	}
	if create {
		id = slugOf(in.Name)
		if id == "" {
			invalid(w, "O nome precisa de letras.", "name")
			return
		}
	}
	m, _ := memberOf(r.Context())
	rules, _ := json.Marshal(in.Rules)
	week, _ := json.Marshal(in.Week)
	d := repo.DietModelRow{ID: id, Name: in.Name, Goal: in.Goal, MealsPerDay: in.MealsPerDay,
		Rules: rules, Week: week, UpdatedBy: m.Name}
	if err := a.Models.SaveDietModel(r.Context(), d, create); err != nil {
		writeRepoErr(w, r, err, "o modelo")
		return
	}
	a.audit(r, "diet_saved", in.Name, "")
	l, err := a.Models.DietModels(r.Context(), id)
	if err != nil || len(l) == 0 {
		apierr.WriteInternal(w, r, err, "Gravado, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, a.viewDiet(l[0], recs))
		return
	}
	ok(w, a.viewDiet(l[0], recs))
}

func (a *API) DietCreate(w http.ResponseWriter, r *http.Request) { a.saveDiet(w, r, "", true) }
func (a *API) DietUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveDiet(w, r, r.PathValue("id"), false)
}

func (a *API) DietStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status  string `json:"status"`
		Comment string `json:"comment"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	if !oneOf(in.Status, "draft", "review", "approved") {
		invalid(w, "Estado desconhecido.", "status")
		return
	}
	m, _ := memberOf(r.Context())
	// Aprovar é da nutrição: é a assinatura profissional do modelo.
	if in.Status == "approved" && !oneOf(m.Role, string(admin.RoleNutrition), string(admin.RoleAdmin)) {
		apierr.Write(w, apierr.Forbidden, "Só a nutrição aprova um modelo de dieta.", "")
		return
	}
	id := r.PathValue("id")
	var comment json.RawMessage
	if c := strings.TrimSpace(in.Comment); c != "" {
		comment, _ = json.Marshal([]map[string]any{{"at": a.now().Format(time.RFC3339), "by": m.Name, "text": c}})
	}
	if err := a.Models.SetDietStatus(r.Context(), id, in.Status, m.Name, comment); err != nil {
		writeRepoErr(w, r, err, "o modelo")
		return
	}
	a.audit(r, "diet_status", id, "Estado: "+in.Status)
	recs, _ := a.recipeIndex(r)
	l, err := a.Models.DietModels(r.Context(), id)
	if err != nil || len(l) == 0 {
		writeRepoErr(w, r, repo.ErrNotFound, "o modelo")
		return
	}
	ok(w, a.viewDiet(l[0], recs))
}
