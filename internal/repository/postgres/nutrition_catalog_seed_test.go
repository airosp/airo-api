package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func catalogoAlimentarCarregado(t *testing.T) (*repo.NutritionCatalogRepo, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}
	r := repo.NewNutritionCatalogRepo(repo.NewTxManager(pool))
	if _, err := r.SeedFoods(ctx); err != nil {
		t.Fatalf("carregar alimentos: %v", err)
	}
	if _, err := r.SeedRecipes(ctx); err != nil {
		t.Fatalf("carregar receitas: %v", err)
	}
	return r, pool, ctx
}

// Todos os alimentos do motor entram, e entram com os mesmos números.
//
// Um catálogo na base que diga outra coisa do que o motor usa é pior do que
// uma tabela vazia: a tabela vazia vê-se, a divergência não.
func TestSeedDeAlimentosEntraInteiro(t *testing.T) {
	_, pool, ctx := catalogoAlimentarCarregado(t)

	catalogo, err := nutrition.Foods()
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM food WHERE active`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(catalogo) {
		t.Fatalf("alimentos na base = %d, no motor = %d", n, len(catalogo))
	}

	for _, f := range catalogo {
		var nome, categoria, orcamento string
		var kcal, proteina, hidratos, gordura float64
		var porcao int
		var estilos, bons []string
		err := pool.QueryRow(ctx,
			`SELECT name, category::text, budget::text, kcal, protein_g, carbs_g, fat_g,
			        serving_g, styles, good_for
			   FROM food WHERE slug = $1`, f.ID).
			Scan(&nome, &categoria, &orcamento, &kcal, &proteina, &hidratos, &gordura,
				&porcao, &estilos, &bons)
		if err != nil {
			t.Fatalf("alimento %q: %v", f.ID, err)
		}
		if nome != f.Name || categoria != string(f.Category) || orcamento != string(f.Budget) {
			t.Errorf("%q: base diz (%s, %s, %s), motor diz (%s, %s, %s)",
				f.ID, nome, categoria, orcamento, f.Name, f.Category, f.Budget)
		}
		if kcal != f.Kcal || proteina != f.Macros.Protein || hidratos != f.Macros.Carbs || gordura != f.Macros.Fat {
			t.Errorf("%q: macros da base (%.1f kcal, %.1f/%.1f/%.1f) ≠ motor (%.1f kcal, %.1f/%.1f/%.1f)",
				f.ID, kcal, proteina, hidratos, gordura,
				f.Kcal, f.Macros.Protein, f.Macros.Carbs, f.Macros.Fat)
		}
		if porcao != int(f.Serving) {
			t.Errorf("%q: porção %d ≠ %d", f.ID, porcao, int(f.Serving))
		}
		if len(estilos) != len(f.Styles) {
			t.Errorf("%q: %d estilos na base, %d no motor", f.ID, len(estilos), len(f.Styles))
		}
		if len(bons) != len(f.GoodFor) {
			t.Errorf("%q: %d slots na base, %d no motor", f.ID, len(bons), len(f.GoodFor))
		}
	}
}

// As receitas entram com os ingredientes ligados ao alimento certo, e com as
// porções que rendem — que é a coluna que faltava e que faz os gramas por
// pessoa saírem ao dobro quando se perde.
func TestSeedDeReceitasLigaIngredientes(t *testing.T) {
	_, pool, ctx := catalogoAlimentarCarregado(t)

	receitas, err := nutrition.Recipes()
	if err != nil {
		t.Fatal(err)
	}

	for _, rec := range receitas {
		var id string
		var serves int
		if err := pool.QueryRow(ctx,
			`SELECT id, serves FROM recipe WHERE slug = $1`, rec.ID).Scan(&id, &serves); err != nil {
			t.Fatalf("receita %q: %v", rec.ID, err)
		}
		if serves != rec.Serves {
			t.Errorf("%q: rende %d na base, %d no motor", rec.ID, serves, rec.Serves)
		}

		rows, err := pool.Query(ctx,
			`SELECT f.slug, ri.grams FROM recipe_ingredient ri
			   JOIN food f ON f.id = ri.food_id
			  WHERE ri.recipe_id = $1`, id)
		if err != nil {
			t.Fatal(err)
		}
		gramas := map[string]int{}
		for rows.Next() {
			var slug string
			var g int
			if err := rows.Scan(&slug, &g); err != nil {
				t.Fatal(err)
			}
			gramas[slug] = g
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		if len(gramas) != len(rec.Ingredients) {
			t.Errorf("%q: %d ingredientes na base, %d no motor", rec.ID, len(gramas), len(rec.Ingredients))
		}
		for _, ing := range rec.Ingredients {
			if gramas[ing.FoodID] != int(ing.Grams) {
				t.Errorf("%q → %q: %d g na base, %d g no motor",
					rec.ID, ing.FoodID, gramas[ing.FoodID], int(ing.Grams))
			}
		}
	}
}

/*
 * Carregar duas vezes não duplica nem deixa lixo.
 *
 * O seed corre a cada arranque com `AIRO_MIGRATE_ON_START` e a cada
 * `migrate up`. Se a segunda corrida duplicasse, o catálogo crescia a cada
 * deploy — e os ingredientes, que são apagados e reescritos, são o sítio onde
 * isso apareceria primeiro.
 */
func TestSeedAlimentarÉIdempotente(t *testing.T) {
	r, pool, ctx := catalogoAlimentarCarregado(t)

	if _, err := r.SeedFoods(ctx); err != nil {
		t.Fatalf("segunda carga de alimentos: %v", err)
	}
	if _, err := r.SeedRecipes(ctx); err != nil {
		t.Fatalf("segunda carga de receitas: %v", err)
	}

	catalogo, err := nutrition.Foods()
	if err != nil {
		t.Fatal(err)
	}
	receitas, err := nutrition.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	ingredientes := 0
	for _, rec := range receitas {
		ingredientes += len(rec.Ingredients)
	}

	var alimentos, nReceitas, nIngredientes int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM food),
		        (SELECT count(*) FROM recipe),
		        (SELECT count(*) FROM recipe_ingredient)`).
		Scan(&alimentos, &nReceitas, &nIngredientes); err != nil {
		t.Fatal(err)
	}
	if alimentos != len(catalogo) || nReceitas != len(receitas) || nIngredientes != ingredientes {
		t.Fatalf("depois de duas cargas: %d alimentos, %d receitas, %d ingredientes — esperado %d/%d/%d",
			alimentos, nReceitas, nIngredientes, len(catalogo), len(receitas), ingredientes)
	}
}

// A tradução de slug para uuid é o que permite guardar um item de refeição.
func TestFoodIDsTraduzSlugs(t *testing.T) {
	r, _, ctx := catalogoAlimentarCarregado(t)

	ids, err := r.FoodIDs(ctx, []string{"chicken", "xima", "nao-existe"})
	if err != nil {
		t.Fatal(err)
	}
	if ids["chicken"] == "" || ids["xima"] == "" {
		t.Fatalf("slugs conhecidos sem id: %v", ids)
	}
	if _, ok := ids["nao-existe"]; ok {
		t.Error("um slug que não existe não devia trazer id")
	}
}
