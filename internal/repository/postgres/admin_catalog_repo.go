package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/jackc/pgx/v5"
)

/*
 * AdminCatalogRepo é o catálogo como a equipa o edita: exercícios, alimentos e
 * receitas.
 *
 * Lê para o formato do motor — `training.Exercise`, `nutrition.Food`,
 * `nutrition.Recipe` — porque é para o motor que a leitura serve: no arranque
 * e depois de cada edição, a API troca o catálogo embutido por este.
 */
type AdminCatalogRepo struct{ tx *TxManager }

func NewAdminCatalogRepo(tx *TxManager) *AdminCatalogRepo { return &AdminCatalogRepo{tx: tx} }

// ExerciseRow é o exercício com o que só o painel precisa.
type ExerciseRow struct {
	training.Exercise
	Instructions      []string
	Contraindications []string
	Active            bool
	EditedAt          *time.Time
}

const exerciseCols = `slug, name, coalesce(pattern, movement_patterns[1], 'core'), movement_patterns,
	primary_muscles, equipment, difficulty::text, impact_level::text, measure::text, cue,
	coalesce(demo_query, ''), coalesce(demo_url, ''), is_warmup, is_cooldown,
	instructions, contraindications, active, admin_edited_at`

func scanExercise(row pgx.Row) (ExerciseRow, error) {
	var e ExerciseRow
	var pattern, level, impact, measure string
	var patterns []string
	err := row.Scan(&e.ID, &e.Name, &pattern, &patterns, &e.Muscles, &e.Equipment, &level, &impact,
		&measure, &e.Cue, &e.DemoQuery, &e.DemoURL, &e.Warmup, &e.Cooldown,
		&e.Instructions, &e.Contraindications, &e.Active, &e.EditedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	e.Pattern = training.MovementPattern(pattern)
	e.Level = training.Experience(level)
	e.Measure = training.Measure(measure)
	// Como o JSON: o impacto baixo e o padrão único ficam implícitos, e é por
	// `ImpactoDe` e `PadroesDe` que o motor os lê.
	if impact != string(training.ImpactoBaixo) {
		e.Impact = training.Impact(impact)
	}
	if !(len(patterns) == 1 && patterns[0] == pattern) && len(patterns) > 0 {
		for _, p := range patterns {
			e.Patterns = append(e.Patterns, training.MovementPattern(p))
		}
	}
	if len(e.Equipment) == 0 {
		e.Equipment = []string{}
	}
	return e, nil
}

// Exercises devolve todos, activos e não, pela ordem do catálogo.
func (r *AdminCatalogRepo) Exercises(ctx context.Context) ([]ExerciseRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT `+exerciseCols+` FROM exercise ORDER BY position NULLS LAST, name`)
	if err != nil {
		return nil, fmt.Errorf("ler exercícios: %w", err)
	}
	defer rows.Close()
	out := []ExerciseRow{}
	for rows.Next() {
		e, err := scanExercise(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *AdminCatalogRepo) Exercise(ctx context.Context, slug string) (ExerciseRow, error) {
	return scanExercise(r.tx.Q(ctx).QueryRow(ctx,
		`SELECT `+exerciseCols+` FROM exercise WHERE slug = $1`, slug))
}

// SaveExercise cria ou actualiza pelo slug. Um exercício novo entra no fim.
func (r *AdminCatalogRepo) SaveExercise(ctx context.Context, e ExerciseRow, create bool) error {
	patterns := training.PadroesDe(e.Exercise)
	ps := make([]string, 0, len(patterns))
	for _, p := range patterns {
		ps = append(ps, string(p))
	}
	intensity := "moderate"
	if e.Pattern == training.Cardio {
		intensity = "high"
	} else if e.Pattern == training.Mobility {
		intensity = "low"
	}
	args := []any{e.ID, e.Name, categoryOf(e.Pattern), ps, training.ObjectivosDe(e.Exercise),
		listaOuVazia(e.Muscles), listaOuVazia(e.Equipment), string(e.Level), intensity,
		string(training.ImpactoDe(e.Exercise)), mechanicsOf(e.Pattern), string(e.Measure),
		e.Warmup, e.Cooldown, e.Cue, e.DemoQuery, e.DemoURL, string(e.Pattern),
		listaOuVazia(e.Instructions), listaOuVazia(e.Contraindications), e.Active}
	if create {
		_, err := r.tx.Q(ctx).Exec(ctx,
			`INSERT INTO exercise (slug, name, category, movement_patterns, goals, primary_muscles,
			   equipment, difficulty, technical_difficulty, physical_difficulty, intensity,
			   impact_level, mechanics, measure, is_warmup, is_cooldown, cue, demo_query, demo_url,
			   pattern, instructions, contraindications, active, admin_edited_at, position)
			 VALUES ($1,$2,$3::exercise_category,$4,$5,$6,$7,$8::difficulty,$8::difficulty,$8::difficulty,
			   $9::intensity,$10::impact_level,$11::mechanics,$12::measure,$13,$14,$15,$16,NULLIF($17,''),
			   $18,$19,$20,$21,now(),(SELECT coalesce(max(position),0)+1 FROM exercise))`, args...)
		if uniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE exercise SET name=$2, category=$3::exercise_category, movement_patterns=$4, goals=$5,
		   primary_muscles=$6, equipment=$7, difficulty=$8::difficulty, technical_difficulty=$8::difficulty,
		   physical_difficulty=$8::difficulty, intensity=$9::intensity, impact_level=$10::impact_level,
		   mechanics=$11::mechanics, measure=$12::measure, is_warmup=$13, is_cooldown=$14, cue=$15,
		   demo_query=$16, demo_url=NULLIF($17,''), pattern=$18, instructions=$19,
		   contraindications=$20, active=$21, admin_edited_at=now()
		 WHERE slug=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ExerciseUsage conta onde um exercício aparece: nos modelos e nos treinos
// feitos nos últimos 30 dias.
func (r *AdminCatalogRepo) ExerciseUsage(ctx context.Context) (templates, people map[string]int, err error) {
	templates, people = map[string]int{}, map[string]int{}
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT item->>'exerciseId', count(DISTINCT t.id)
		   FROM workout_template t,
		        LATERAL jsonb_array_elements(coalesce(t.blocks->'warmup','[]') || coalesce(t.blocks->'main','[]') || coalesce(t.blocks->'cooldown','[]')) item
		  GROUP BY 1`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		templates[id] = n
	}
	rows.Close()
	rows, err = r.tx.Q(ctx).Query(ctx,
		`SELECT e.slug, count(DISTINCT s.user_id)
		   FROM exercise_prescription p
		   JOIN workout_session s ON s.id = p.session_id
		   JOIN exercise e ON e.id = p.exercise_id
		  WHERE s.occurred_at > now() - interval '30 days'
		  GROUP BY 1`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, nil, err
		}
		people[id] = n
	}
	return templates, people, rows.Err()
}

// ── Alimentos ────────────────────────────────────────────────────────────────

type FoodRow struct {
	nutrition.Food
	Fiber    *float64
	Active   bool
	EditedAt *time.Time
}

const foodCols = `slug, name, category::text, kcal::float8, protein_g::float8, carbs_g::float8, fat_g::float8,
	fiber_g::float8, serving_g, styles, budget::text, good_for, coalesce(region, ''), active, admin_edited_at`

func scanFood(row pgx.Row) (FoodRow, error) {
	var f FoodRow
	var cat, budget string
	var styles, goodFor []string
	var serving int
	err := row.Scan(&f.ID, &f.Name, &cat, &f.Kcal, &f.Macros.Protein, &f.Macros.Carbs, &f.Macros.Fat,
		&f.Fiber, &serving, &styles, &budget, &goodFor, &f.Region, &f.Active, &f.EditedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	f.Category = nutrition.FoodCategory(cat)
	f.Budget = nutrition.Budget(budget)
	f.Serving = float64(serving)
	for _, s := range styles {
		f.Styles = append(f.Styles, nutrition.DietStyle(s))
	}
	for _, s := range goodFor {
		f.GoodFor = append(f.GoodFor, nutrition.Slot(s))
	}
	return f, nil
}

func (r *AdminCatalogRepo) Foods(ctx context.Context) ([]FoodRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT `+foodCols+` FROM food ORDER BY position NULLS LAST, name`)
	if err != nil {
		return nil, fmt.Errorf("ler alimentos: %w", err)
	}
	defer rows.Close()
	out := []FoodRow{}
	for rows.Next() {
		f, err := scanFood(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *AdminCatalogRepo) Food(ctx context.Context, slug string) (FoodRow, error) {
	return scanFood(r.tx.Q(ctx).QueryRow(ctx, `SELECT `+foodCols+` FROM food WHERE slug = $1`, slug))
}

func (r *AdminCatalogRepo) SaveFood(ctx context.Context, f FoodRow, create bool) error {
	var regiao *string
	if f.Region != "" {
		v := f.Region
		regiao = &v
	}
	args := []any{f.ID, f.Name, string(f.Category), f.Kcal, f.Macros.Protein, f.Macros.Carbs,
		f.Macros.Fat, f.Fiber, int(f.Serving), estilosDe(f.Styles), string(f.Budget),
		slotsDe(f.GoodFor), regiao, f.Active}
	if create {
		_, err := r.tx.Q(ctx).Exec(ctx,
			`INSERT INTO food (slug, name, category, kcal, protein_g, carbs_g, fat_g, fiber_g, serving_g,
			   styles, budget, good_for, region, active, admin_edited_at, position)
			 VALUES ($1,$2,$3::food_category,$4,$5,$6,$7,$8,$9,$10,$11::budget,$12,$13,$14,now(),
			   (SELECT coalesce(max(position),0)+1 FROM food))`, args...)
		if uniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE food SET name=$2, category=$3::food_category, kcal=$4, protein_g=$5, carbs_g=$6, fat_g=$7,
		   fiber_g=$8, serving_g=$9, styles=$10, budget=$11::budget, good_for=$12, region=$13,
		   active=$14, admin_edited_at=now()
		 WHERE slug=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Receitas ─────────────────────────────────────────────────────────────────

type RecipeRow struct {
	nutrition.Recipe
	Active      bool
	ValidatedBy *string
	ValidatedAt *time.Time
	EditedAt    *time.Time
}

func (r *AdminCatalogRepo) Recipes(ctx context.Context) ([]RecipeRow, error) {
	return r.recipes(ctx, "")
}

func (r *AdminCatalogRepo) Recipe(ctx context.Context, slug string) (RecipeRow, error) {
	l, err := r.recipes(ctx, slug)
	if err != nil {
		return RecipeRow{}, err
	}
	if len(l) == 0 {
		return RecipeRow{}, ErrNotFound
	}
	return l[0], nil
}

func (r *AdminCatalogRepo) recipes(ctx context.Context, slug string) ([]RecipeRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT r.slug, r.name, r.slots, r.styles, r.serves, r.active, r.validated_by, r.validated_at,
		        r.admin_edited_at,
		        coalesce(array_agg(f.slug ORDER BY i.position, f.slug) FILTER (WHERE f.slug IS NOT NULL), '{}'),
		        coalesce(array_agg(i.grams ORDER BY i.position, f.slug) FILTER (WHERE f.slug IS NOT NULL), '{}')
		   FROM recipe r
		   LEFT JOIN recipe_ingredient i ON i.recipe_id = r.id
		   LEFT JOIN food f ON f.id = i.food_id
		  WHERE ($1 = '' OR r.slug = $1)
		  GROUP BY r.id
		  ORDER BY r.position NULLS LAST, r.name`, slug)
	if err != nil {
		return nil, fmt.Errorf("ler receitas: %w", err)
	}
	defer rows.Close()
	out := []RecipeRow{}
	for rows.Next() {
		var rec RecipeRow
		var slots, styles, foods []string
		var grams []int16
		if err := rows.Scan(&rec.ID, &rec.Name, &slots, &styles, &rec.Serves, &rec.Active,
			&rec.ValidatedBy, &rec.ValidatedAt, &rec.EditedAt, &foods, &grams); err != nil {
			return nil, err
		}
		for _, s := range slots {
			rec.Slots = append(rec.Slots, nutrition.Slot(s))
		}
		for _, s := range styles {
			rec.Styles = append(rec.Styles, nutrition.DietStyle(s))
		}
		rec.Ingredients = []nutrition.RecipeIngredient{}
		for i := range foods {
			rec.Ingredients = append(rec.Ingredients, nutrition.RecipeIngredient{FoodID: foods[i], Grams: float64(grams[i])})
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SaveRecipe grava a receita e reescreve os ingredientes, na ordem dada.
// Editar tira a validação: um prato mudado é um prato por rever.
func (r *AdminCatalogRepo) SaveRecipe(ctx context.Context, rec RecipeRow, create bool) error {
	return r.tx.Do(ctx, func(ctx context.Context) error {
		q := r.tx.Q(ctx)
		var id string
		var err error
		if create {
			err = q.QueryRow(ctx,
				`INSERT INTO recipe (slug, name, slots, styles, serves, active, admin_edited_at, position)
				 VALUES ($1,$2,$3,$4,$5,$6,now(),(SELECT coalesce(max(position),0)+1 FROM recipe))
				 RETURNING id`,
				rec.ID, rec.Name, slotsDe(rec.Slots), estilosDe(rec.Styles), rec.Serves, rec.Active).Scan(&id)
			if uniqueViolation(err) {
				return ErrDuplicate
			}
		} else {
			err = q.QueryRow(ctx,
				`UPDATE recipe SET name=$2, slots=$3, styles=$4, serves=$5, active=$6,
				   admin_edited_at=now(), validated_by=NULL, validated_at=NULL
				 WHERE slug=$1 RETURNING id`,
				rec.ID, rec.Name, slotsDe(rec.Slots), estilosDe(rec.Styles), rec.Serves, rec.Active).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
		}
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM recipe_ingredient WHERE recipe_id = $1`, id); err != nil {
			return err
		}
		for pos, ing := range rec.Ingredients {
			tag, err := q.Exec(ctx,
				`INSERT INTO recipe_ingredient (recipe_id, food_id, grams, position)
				 SELECT $1, f.id, $3, $4 FROM food f WHERE f.slug = $2`,
				id, ing.FoodID, int(ing.Grams), pos+1)
			if uniqueViolation(err) {
				return fmt.Errorf("%w: ingrediente repetido %q", ErrDuplicate, ing.FoodID)
			}
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: alimento %q", ErrNotFound, ing.FoodID)
			}
		}
		return nil
	})
}

func (r *AdminCatalogRepo) ValidateRecipe(ctx context.Context, slug, by string) error {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE recipe SET validated_by = $2, validated_at = now() WHERE slug = $1`, slug, by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Para o motor ─────────────────────────────────────────────────────────────

// EngineCatalog lê os três catálogos activos, prontos a entregar ao motor.
func (r *AdminCatalogRepo) EngineCatalog(ctx context.Context) ([]training.Exercise, []nutrition.Food, []nutrition.Recipe, error) {
	exs, err := r.Exercises(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	fs, err := r.Foods(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	rs, err := r.Recipes(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var outE []training.Exercise
	for _, e := range exs {
		if e.Active {
			outE = append(outE, e.Exercise)
		}
	}
	var outF []nutrition.Food
	activos := map[string]bool{}
	for _, f := range fs {
		if f.Active {
			outF = append(outF, f.Food)
			activos[f.ID] = true
		}
	}
	outR := []nutrition.Recipe{}
	for _, rec := range rs {
		if !rec.Active {
			continue
		}
		// Uma receita com um alimento retirado não se cozinha.
		ok := len(rec.Ingredients) > 0
		for _, ing := range rec.Ingredients {
			ok = ok && activos[ing.FoodID]
		}
		if ok {
			outR = append(outR, rec.Recipe)
		}
	}
	return outE, outF, outR, nil
}
