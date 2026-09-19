package postgres

import (
	"context"
	"fmt"

	"github.com/airosp/airo-api/internal/engine/nutrition"
)

/*
 * NutritionCatalogRepo carrega os alimentos e as receitas para a base.
 *
 * ⚠️ As tabelas `food`, `recipe` e `recipe_ingredient` existem desde o
 * `0001_init.sql` e estiveram vazias todo este tempo. Não era um esquecimento
 * inofensivo: `meal_item.food_id` aponta para `food`, e por isso **nenhum
 * plano alimentar podia ser guardado** — a chave estrangeira não tinha para
 * onde apontar. Um dia de refeições era montado, mostrado e esquecido.
 *
 * O JSON continua a ser a fonte: é o mesmo ficheiro que o motor lê e que o
 * cliente compara no `npm run catalogo`. Aqui não nasce catálogo nenhum —
 * copia-se o que já existe, como o `CatalogRepo` faz com os exercícios.
 */
type NutritionCatalogRepo struct{ tx *TxManager }

func NewNutritionCatalogRepo(tx *TxManager) *NutritionCatalogRepo {
	return &NutritionCatalogRepo{tx: tx}
}

// SeedFoods carrega a base de alimentos. Idempotente pelo slug: correr duas
// vezes actualiza, não duplica.
func (r *NutritionCatalogRepo) SeedFoods(ctx context.Context) (int, error) {
	lista, err := nutrition.Foods()
	if err != nil {
		return 0, err
	}
	q := r.tx.Q(ctx)

	for _, f := range lista {
		// `region` é texto anulável no esquema e vazio no catálogo quando o
		// alimento não é de sítio nenhum. Guardar `''` fingiria uma região
		// chamada "nada"; o `NULL` diz o que se passa.
		var regiao *string
		if f.Region != "" {
			valor := f.Region
			regiao = &valor
		}
		_, err := q.Exec(ctx,
			`INSERT INTO food (slug, name, category, kcal, protein_g, carbs_g, fat_g,
			                   serving_g, styles, budget, good_for, region)
			 VALUES ($1,$2,$3::food_category,$4,$5,$6,$7,$8,$9,$10::budget,$11,$12)
			 ON CONFLICT (slug) DO UPDATE SET
			   name = EXCLUDED.name, category = EXCLUDED.category, kcal = EXCLUDED.kcal,
			   protein_g = EXCLUDED.protein_g, carbs_g = EXCLUDED.carbs_g,
			   fat_g = EXCLUDED.fat_g, serving_g = EXCLUDED.serving_g,
			   styles = EXCLUDED.styles, budget = EXCLUDED.budget,
			   good_for = EXCLUDED.good_for, region = EXCLUDED.region,
			   active = true`,
			f.ID, f.Name, string(f.Category), f.Kcal,
			f.Macros.Protein, f.Macros.Carbs, f.Macros.Fat, int(f.Serving),
			estilosDe(f.Styles), string(f.Budget), slotsDe(f.GoodFor), regiao)
		if err != nil {
			return 0, fmt.Errorf("carregar alimento %q: %w", f.ID, err)
		}
	}
	return len(lista), nil
}

/*
 * SeedRecipes carrega as receitas e os seus ingredientes.
 *
 * Corre **depois** dos alimentos: um ingrediente aponta para `food` por chave
 * estrangeira, e ao contrário a primeira receita não teria onde encaixar.
 *
 * Os ingredientes são apagados e reescritos a cada carga. É a única forma
 * honesta de remover um ingrediente que saiu do JSON: um `ON CONFLICT` sobre
 * os que ficam deixaria o que saiu para trás, e a receita na base passava a
 * ser uma receita diferente da que o motor cozinha.
 */
func (r *NutritionCatalogRepo) SeedRecipes(ctx context.Context) (int, error) {
	lista, err := nutrition.Recipes()
	if err != nil {
		return 0, err
	}
	q := r.tx.Q(ctx)

	for _, rec := range lista {
		var id string
		err := q.QueryRow(ctx,
			`INSERT INTO recipe (slug, name, slots, styles, serves)
			 VALUES ($1,$2,$3,$4,$5)
			 ON CONFLICT (slug) DO UPDATE SET
			   name = EXCLUDED.name, slots = EXCLUDED.slots,
			   styles = EXCLUDED.styles, serves = EXCLUDED.serves, active = true
			 RETURNING id`,
			rec.ID, rec.Name, slotsDe(rec.Slots), estilosDe(rec.Styles), rec.Serves).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("carregar receita %q: %w", rec.ID, err)
		}

		if _, err := q.Exec(ctx, `DELETE FROM recipe_ingredient WHERE recipe_id = $1`, id); err != nil {
			return 0, fmt.Errorf("limpar ingredientes de %q: %w", rec.ID, err)
		}

		for _, ing := range rec.Ingredients {
			_, err := q.Exec(ctx,
				`INSERT INTO recipe_ingredient (recipe_id, food_id, grams)
				 SELECT $1, f.id, $3 FROM food f WHERE f.slug = $2`,
				id, ing.FoodID, int(ing.Grams))
			if err != nil {
				return 0, fmt.Errorf("ingrediente %q de %q: %w", ing.FoodID, rec.ID, err)
			}
		}
	}
	return len(lista), nil
}

/*
 * FoodIDs traduz slugs do catálogo para os uuids da tabela.
 *
 * O motor fala em `chicken`; o esquema guarda chaves estrangeiras. Sem esta
 * tradução, guardar um item de refeição obrigava cada sítio a fazer o seu
 * `SELECT` — e a esquecer-se dele exactamente uma vez.
 */
func (r *NutritionCatalogRepo) FoodIDs(ctx context.Context, slugs []string) (map[string]string, error) {
	if len(slugs) == 0 {
		return map[string]string{}, nil
	}
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT slug, id FROM food WHERE slug = ANY($1)`, slugs)
	if err != nil {
		return nil, fmt.Errorf("ler catálogo de alimentos: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}

// estilosDe e slotsDe passam os tipos do motor para o `text[]` do esquema.
// Nunca `nil`: a coluna é `NOT NULL DEFAULT '{}'`, e um `nil` viola-a em vez
// de cair no valor por omissão.
func estilosDe(v []nutrition.DietStyle) []string {
	out := make([]string, 0, len(v))
	for _, s := range v {
		out = append(out, string(s))
	}
	return out
}

func slotsDe(v []nutrition.Slot) []string {
	out := make([]string, 0, len(v))
	for _, s := range v {
		out = append(out, string(s))
	}
	return out
}
