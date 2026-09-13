package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// Catalog serve os catálogos de exercícios e alimentos.
//
// Existem porque hoje a app os transporta: 60 exercícios e centenas de
// alimentos embutidos no pacote. Funciona — e deixa de funcionar no dia em que
// se corrige um valor nutricional, porque a correcção só chega a quem
// actualizar a aplicação.
//
// Servi-los daqui não é para tirar o catálogo local: é para o **poder**
// actualizar sem uma versão nova.
type Catalog struct {
	Training  training.Config
	Nutrition nutrition.Config
}

// Exercises devolve o catálogo, com filtros opcionais.
func (h Catalog) Exercises(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	todos, err := training.Library()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o catálogo.")
		return
	}

	q := r.URL.Query()
	pattern := q.Get("pattern")
	equipment := listaDe(q.Get("equipment"))
	level := q.Get("level")

	out := make([]exerciseView, 0, len(todos))
	for _, e := range todos {
		if pattern != "" && string(e.Pattern) != pattern {
			continue
		}
		if level != "" && string(e.Level) != level {
			continue
		}
		// Equipamento: basta ter **um** dos que a pessoa tem. Um exercício sem
		// equipamento é só o corpo, e está sempre disponível.
		if len(equipment) > 0 && len(e.Equipment) > 0 && !algumEm(e.Equipment, equipment) {
			continue
		}
		out = append(out, paraExercicio(e))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"exercises": out, "total": len(out)})
}

// Exercise devolve um exercício.
func (h Catalog) Exercise(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	e, ok := training.GetExercise(r.PathValue("id"))
	if !ok {
		apierr.Write(w, apierr.NotFound, "Esse exercício não existe.", "id")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, paraExercicio(e))
}

// Foods devolve os alimentos compatíveis com o estilo e o orçamento de quem
// pergunta.
func (h Catalog) Foods(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	q := r.URL.Query()
	in := nutrition.AvailableFoodsInput{
		Style:      nutrition.DietStyle(orDefault(q.Get("style"), "omnivore")),
		Budget:     nutrition.Budget(orDefault(q.Get("budget"), "high")),
		Exclusions: listaDe(q.Get("exclude")),
		Category:   nutrition.FoodCategory(q.Get("category")),
		Slot:       nutrition.Slot(q.Get("slot")),
	}
	alimentos, err := nutrition.AvailableFoods(in)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o catálogo.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"foods": paraAlimentos(alimentos), "total": len(alimentos),
	})
}

// FoodSubstitutes devolve alternativas com a **porção recalculada**.
//
// É por isso que a interface escreve "Porção equivalente: 170 g" e não apenas
// "170 g": a troca preserva a função do alimento na refeição, e a porção muda
// para isso acontecer.
func (h Catalog) FoodSubstitutes(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	food, ok := nutrition.GetFood(r.PathValue("id"))
	if !ok {
		apierr.Write(w, apierr.NotFound, "Esse alimento não existe.", "id")
		return
	}

	q := r.URL.Query()
	gramas, _ := strconv.ParseFloat(q.Get("grams"), 64)
	if gramas <= 0 {
		gramas = food.Serving
	}

	subs, err := nutrition.FindSubstitutions(nutrition.MealItem{
		FoodID: food.ID, Name: food.Name, Grams: gramas,
		Kcal: food.Kcal * gramas / 100,
		Macros: nutrition.MacrosFloat{
			Protein: food.Macros.Protein * gramas / 100,
			Carbs:   food.Macros.Carbs * gramas / 100,
			Fat:     food.Macros.Fat * gramas / 100,
		},
	}, nutrition.DietProfile{
		Style:      nutrition.DietStyle(orDefault(q.Get("style"), "omnivore")),
		Budget:     nutrition.Budget(orDefault(q.Get("budget"), "high")),
		Exclusions: listaDe(q.Get("exclude")),
	}, limiteDe(q.Get("limit")))
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível procurar alternativas.")
		return
	}

	out := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		out = append(out, map[string]any{
			"foodId": s.Food.ID, "name": s.Food.Name,
			"grams": s.Grams, "kcal": s.Kcal,
			// Match de 0 a 1: quão perto fica em calorias e proteína.
			"match": s.Match,
			// A frase que a interface mostra, já escrita: "porção equivalente"
			// é o que explica porque é que as gramas mudaram.
			"portionLabel": "Porção equivalente: " + strconv.Itoa(s.Grams) + " g",
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"substitutions": out})
}

type exerciseView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Muscles   []string `json:"muscles"`
	Equipment []string `json:"equipment"`
	Pattern   string   `json:"pattern"`
	Measure   string   `json:"measure"`
	Cue       string   `json:"cue"`
	Level     string   `json:"level"`
	Warmup    bool     `json:"warmup,omitempty"`
	Cooldown  bool     `json:"cooldown,omitempty"`
}

func paraExercicio(e training.Exercise) exerciseView {
	return exerciseView{
		ID: e.ID, Name: e.Name, Muscles: nonNilStrings(e.Muscles),
		Equipment: nonNilStrings(e.Equipment), Pattern: string(e.Pattern),
		Measure: string(e.Measure), Cue: e.Cue, Level: string(e.Level),
		Warmup: e.Warmup, Cooldown: e.Cooldown,
	}
}

func paraAlimentos(fs []nutrition.Food) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"id": f.ID, "name": f.Name, "category": string(f.Category),
			// Por 100 g, sempre: normalizar aqui evita converter em vinte sítios.
			"kcal": f.Kcal, "proteinG": f.Macros.Protein,
			"carbsG": f.Macros.Carbs, "fatG": f.Macros.Fat,
			"servingG": f.Serving, "budget": string(f.Budget),
		})
	}
	return out
}

// JSON com `null` onde a app espera uma lista faz `map` rebentar no cliente.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func listaDe(raw string) []string {
	if raw == "" {
		return nil
	}
	out := strings.Split(raw, ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

func algumEm(tem, disponivel []string) bool {
	for _, t := range tem {
		for _, d := range disponivel {
			if t == d {
				return true
			}
		}
	}
	return false
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// limiteDe lê quantas alternativas mostrar. Zero deixa o motor decidir — que
// escolhe 4, porque uma lista de vinte trocas não é uma escolha, é um catálogo.
func limiteDe(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	if n > 20 {
		return 20
	}
	return n
}
