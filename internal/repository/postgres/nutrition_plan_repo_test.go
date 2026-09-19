package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
 * O plano do dia, guardado.
 *
 * ⚠️ `daily_plan`, `planned_meal` e `meal_item` estiveram vazias desde o
 * primeiro dia. O que estes testes protegem não é a escrita por si: é o dia
 * passado continuar a ser o dia que foi, depois de a estratégia mudar.
 */

func baseComEstrategia(t *testing.T) (*pgxpool.Pool, context.Context, string, string) {
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
	// Sem catálogo não há `food_id` para onde apontar — que era exactamente a
	// razão por que estas tabelas não podiam ser escritas.
	if _, err := repo.NewNutritionCatalogRepo(repo.NewTxManager(pool)).SeedFoods(ctx); err != nil {
		t.Fatal(err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841113000','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var strategyID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nutrition_strategy (user_id, goal, calorie_target, protein_g, carbs_g, fat_g,
		                                 tdee_estimated, effective_from)
		 VALUES ($1,'lose_fat',2100,150,200,60,2400,'2026-09-01') RETURNING id`,
		userID).Scan(&strategyID); err != nil {
		t.Fatal(err)
	}
	return pool, ctx, userID, strategyID
}

func planoDeExemplo(dia string) nutrition.DayPlan {
	item := func(slug string, gramas float64) nutrition.MealItem {
		it, ok := nutrition.ItemFromFood(slug, gramas)
		if !ok {
			panic("alimento fora do catálogo: " + slug)
		}
		return it
	}
	return nutrition.DayPlan{
		DayISO: dia,
		Kcal:   2100,
		Macros: nutrition.Macros{Protein: 150, Carbs: 200, Fat: 60},
		Meals: []nutrition.PlannedMeal{
			{
				ID: dia + "-breakfast", Slot: nutrition.Breakfast, Title: "Pequeno-almoço",
				Role: "balanced", Kcal: 500,
				Macros: nutrition.MacrosFloat{Protein: 30, Carbs: 60, Fat: 14},
				Items:  []nutrition.MealItem{item("eggs", 120), item("bread", 80)},
			},
			{
				ID: dia + "-lunch", Slot: nutrition.Lunch, Title: "Almoço",
				Role: "post_workout", Kcal: 800,
				Macros: nutrition.MacrosFloat{Protein: 60, Carbs: 80, Fat: 20},
				Items:  []nutrition.MealItem{item("chicken", 150), item("rice", 200)},
			},
		},
	}
}

// O que se guardou é o que se lê — item a item, e com o alvo de cada refeição.
func TestPlanoDoDiaVoltaComoFoiGuardado(t *testing.T) {
	pool, ctx, userID, strategyID := baseComEstrategia(t)
	r := repo.NewNutritionPlanRepo(repo.NewTxManager(pool))

	dia := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	original := planoDeExemplo("2026-09-15")
	if err := r.SaveDay(ctx, userID, strategyID, dia, original); err != nil {
		t.Fatalf("guardar: %v", err)
	}

	lido, err := r.Day(ctx, userID, dia)
	if err != nil {
		t.Fatalf("ler: %v", err)
	}
	if lido.StrategyID != strategyID {
		t.Errorf("estratégia %q ≠ %q", lido.StrategyID, strategyID)
	}
	if lido.Plan.Kcal != original.Kcal || lido.Plan.Macros != original.Macros {
		t.Errorf("total do dia %d kcal %+v ≠ %d kcal %+v",
			lido.Plan.Kcal, lido.Plan.Macros, original.Kcal, original.Macros)
	}
	if len(lido.Plan.Meals) != len(original.Meals) {
		t.Fatalf("%d refeições ≠ %d", len(lido.Plan.Meals), len(original.Meals))
	}
	for i, m := range original.Meals {
		got := lido.Plan.Meals[i]
		if got.ID != m.ID || got.Slot != m.Slot || got.Title != m.Title || got.Role != m.Role {
			t.Errorf("refeição %d: (%s,%s,%s,%s) ≠ (%s,%s,%s,%s)",
				i, got.ID, got.Slot, got.Title, got.Role, m.ID, m.Slot, m.Title, m.Role)
		}
		if got.Kcal != m.Kcal || got.Macros != m.Macros {
			t.Errorf("refeição %s: %d kcal %+v ≠ %d kcal %+v",
				m.Slot, got.Kcal, got.Macros, m.Kcal, m.Macros)
		}
		if len(got.Items) != len(m.Items) {
			t.Fatalf("refeição %s: %d itens ≠ %d", m.Slot, len(got.Items), len(m.Items))
		}
		for j, it := range m.Items {
			if got.Items[j] != it {
				t.Errorf("refeição %s item %d: %+v ≠ %+v", m.Slot, j, got.Items[j], it)
			}
		}
	}
}

/*
 * Guardar outra vez substitui — não acumula.
 *
 * É o caminho de uma troca: a refeição muda e o dia é regravado. Sem o
 * `DELETE` antes da escrita ficavam os dois pratos na mesma ranhura, e a
 * leitura seguinte trazia um almoço com o dobro da comida.
 */
func TestGuardarODiaOutraVezSubstitui(t *testing.T) {
	pool, ctx, userID, strategyID := baseComEstrategia(t)
	r := repo.NewNutritionPlanRepo(repo.NewTxManager(pool))
	dia := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	if err := r.SaveDay(ctx, userID, strategyID, dia, planoDeExemplo("2026-09-15")); err != nil {
		t.Fatal(err)
	}

	trocado := planoDeExemplo("2026-09-15")
	item, _ := nutrition.ItemFromFood("beans", 180)
	trocado.Meals[1].Items = []nutrition.MealItem{item}
	if err := r.SaveDay(ctx, userID, strategyID, dia, trocado); err != nil {
		t.Fatal(err)
	}

	var planos, refeicoes, itens int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM daily_plan),
		        (SELECT count(*) FROM planned_meal),
		        (SELECT count(*) FROM meal_item)`).Scan(&planos, &refeicoes, &itens); err != nil {
		t.Fatal(err)
	}
	if planos != 1 || refeicoes != 2 || itens != 3 {
		t.Fatalf("depois de duas gravações: %d planos, %d refeições, %d itens — esperado 1/2/3",
			planos, refeicoes, itens)
	}

	lido, err := r.Day(ctx, userID, dia)
	if err != nil {
		t.Fatal(err)
	}
	if len(lido.Plan.Meals[1].Items) != 1 || lido.Plan.Meals[1].Items[0].FoodID != "beans" {
		t.Errorf("o almoço devia ter só o que foi gravado por último: %+v", lido.Plan.Meals[1].Items)
	}
}

// Um dia que nunca foi guardado diz que não existe — e não devolve um dia vazio,
// que quem lê mostraria como um dia sem refeições nenhumas.
func TestDiaNuncaGuardadoNaoExiste(t *testing.T) {
	pool, ctx, userID, _ := baseComEstrategia(t)
	r := repo.NewNutritionPlanRepo(repo.NewTxManager(pool))

	_, err := r.Day(ctx, userID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("esperava ErrNotFound, veio %v", err)
	}
}

// Apagar a conta leva o plano com ela: `daily_plan` tem `ON DELETE CASCADE`
// desde o início, e o trilho de apagar a conta é testado em conta própria —
// aqui verifica-se que estas três tabelas novas não ficam para trás.
func TestApagarContaLevaOPlanoDoDia(t *testing.T) {
	pool, ctx, userID, strategyID := baseComEstrategia(t)
	r := repo.NewNutritionPlanRepo(repo.NewTxManager(pool))
	dia := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if err := r.SaveDay(ctx, userID, strategyID, dia, planoDeExemplo("2026-09-15")); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}

	var planos, refeicoes, itens int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM daily_plan),
		        (SELECT count(*) FROM planned_meal),
		        (SELECT count(*) FROM meal_item)`).Scan(&planos, &refeicoes, &itens); err != nil {
		t.Fatal(err)
	}
	if planos+refeicoes+itens != 0 {
		t.Fatalf("ficaram %d planos, %d refeições e %d itens órfãos", planos, refeicoes, itens)
	}
}
