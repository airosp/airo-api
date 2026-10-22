package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"reflect"
	"testing"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/migrations"
)

func catalogoCarregado(t *testing.T) (*repo.AdminCatalogRepo, *repo.TxManager, context.Context) {
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
	tx := repo.NewTxManager(pool)
	if _, err := repo.NewCatalogRepo(tx).SeedExercises(ctx); err != nil {
		t.Fatal(err)
	}
	nc := repo.NewNutritionCatalogRepo(tx)
	if _, err := nc.SeedFoods(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := nc.SeedRecipes(ctx); err != nil {
		t.Fatal(err)
	}
	return repo.NewAdminCatalogRepo(tx), tx, ctx
}

/*
 * A ida e volta pela base de dados não pode mudar o catálogo.
 *
 * É a condição para o motor passar a ler o gravado: se o que volta não for
 * igual ao JSON, o treino e a ementa de toda a gente mudavam no dia do deploy
 * sem ninguém ter editado nada.
 */
func TestCatalogoGravadoIgualAoEmbutido(t *testing.T) {
	r, _, ctx := catalogoCarregado(t)
	exs, foods, recs, err := r.EngineCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want, _ := training.EmbeddedLibrary()
	if len(exs) != len(want) {
		t.Fatalf("exercícios: %d, quero %d", len(exs), len(want))
	}
	for i := range want {
		w := want[i]
		if len(w.Equipment) == 0 {
			w.Equipment = []string{}
		}
		if !reflect.DeepEqual(exs[i], w) {
			t.Fatalf("exercício %d difere:\n got %+v\nwant %+v", i, exs[i], w)
		}
	}

	wantF, _ := nutrition.EmbeddedFoods()
	if len(foods) != len(wantF) {
		t.Fatalf("alimentos: %d, quero %d", len(foods), len(wantF))
	}
	for i := range wantF {
		if !reflect.DeepEqual(foods[i], wantF[i]) {
			t.Fatalf("alimento %d difere:\n got %+v\nwant %+v", i, foods[i], wantF[i])
		}
	}

	wantR, _ := nutrition.EmbeddedRecipes()
	if len(recs) != len(wantR) {
		t.Fatalf("receitas: %d, quero %d", len(recs), len(wantR))
	}
	for i := range wantR {
		if !reflect.DeepEqual(recs[i], wantR[i]) {
			t.Fatalf("receita %d difere:\n got %+v\nwant %+v", i, recs[i], wantR[i])
		}
	}
}

// O que a equipa editou sobrevive a um novo `migrate up`.
func TestSeedNaoPisaEdicao(t *testing.T) {
	r, tx, ctx := catalogoCarregado(t)
	e, err := r.Exercise(ctx, "pushup")
	if err != nil {
		t.Fatal(err)
	}
	e.Cue = "Editado no painel."
	if err := r.SaveExercise(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Food(ctx, "chicken")
	f.Kcal = 170
	if err := r.SaveFood(ctx, f, false); err != nil {
		t.Fatal(err)
	}
	rec, _ := r.Recipe(ctx, "matapa_com_xima")
	rec.Ingredients = rec.Ingredients[:1]
	if err := r.SaveRecipe(ctx, rec, false); err != nil {
		t.Fatal(err)
	}

	// Volta a correr o seed como num deploy.
	if _, err := repo.NewCatalogRepo(tx).SeedExercises(ctx); err != nil {
		t.Fatal(err)
	}
	nc := repo.NewNutritionCatalogRepo(tx)
	if _, err := nc.SeedFoods(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := nc.SeedRecipes(ctx); err != nil {
		t.Fatal(err)
	}
	if e2, _ := r.Exercise(ctx, "pushup"); e2.Cue != "Editado no painel." {
		t.Fatalf("o seed pisou a edição: %q", e2.Cue)
	}
	if f2, _ := r.Food(ctx, "chicken"); f2.Kcal != 170 {
		t.Fatalf("o seed pisou o alimento: %v", f2.Kcal)
	}
	if r2, _ := r.Recipe(ctx, "matapa_com_xima"); len(r2.Ingredients) != 1 {
		t.Fatalf("o seed pisou os ingredientes: %d", len(r2.Ingredients))
	}
}
