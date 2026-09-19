package service_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
 * O plano de um dia que já passou não se remonta.
 *
 * ⚠️ Era este o defeito: o dia era função da estratégia **de agora**. Bastava
 * mudar de objectivo para o histórico inteiro passar a mostrar os pratos e as
 * calorias de hoje — ao lado de um diário que registava outra coisa. Duas
 * linhas no mesmo ecrã a descrever o mesmo dia e a discordar.
 */

func baseNutricao(t *testing.T, agora time.Time) (*service.NutritionService, *pgxpool.Pool, context.Context, string) {
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
	if _, err := repo.NewNutritionCatalogRepo(tx).SeedFoods(ctx); err != nil {
		t.Fatal(err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841114000','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	svc := service.NewNutritionService(
		repo.NewGoalRepo(tx), repo.NewMealPrefRepo(tx),
		nutrition.DefaultConfig(), goal.DefaultConfig(),
	).ComPlanoGuardado(repo.NewNutritionPlanRepo(tx), clock.NewFixed(agora))

	return svc, pool, ctx, userID
}

func estrategia(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID string, kcal int, desde string) {
	t.Helper()
	// Uma estratégia nova fecha a anterior: é `CurrentStrategy` que escolhe a
	// que vale no dia, e duas abertas ao mesmo tempo tornavam essa escolha
	// dependente da ordem das linhas.
	if _, err := pool.Exec(ctx,
		`UPDATE nutrition_strategy SET effective_to = $2::date - 1
		   WHERE user_id = $1 AND effective_to IS NULL`, userID, desde); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO nutrition_strategy (user_id, goal, calorie_target, protein_g, carbs_g, fat_g,
		                                 tdee_estimated, effective_from)
		 VALUES ($1,'lose_fat',$2,150,200,60,2400,$3::date)`,
		userID, kcal, desde); err != nil {
		t.Fatal(err)
	}
}

func pedido(userID string, dia time.Time) service.NutritionTodayInput {
	return service.NutritionTodayInput{
		UserID: userID,
		Diet: nutrition.DietProfile{
			Style: nutrition.Omnivore, MealsPerDay: 4, Budget: nutrition.BudgetMedium,
		},
		Training: nutrition.TrainingLoad{SessionsPerWeek: 3, WorkoutTime: "evening"},
		LocalDay: dia,
		WeightKg: 80,
	}
}

func TestDiaPassadoNaoÉRemontadoComAEstrategiaDeHoje(t *testing.T) {
	ontem := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	svc, pool, ctx, userID := baseNutricao(t, time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC))

	estrategia(t, pool, ctx, userID, 2100, "2026-09-01")

	// Dia 19, vivido: monta-se e fica escrito.
	vivido, err := svc.Today(ctx, pedido(userID, ontem))
	if err != nil {
		t.Fatalf("dia vivido: %v", err)
	}
	if vivido.Day.Kcal != 2100 {
		t.Fatalf("o dia vivido devia valer 2100 kcal, veio %d", vivido.Day.Kcal)
	}

	var guardados int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM daily_plan`).Scan(&guardados); err != nil {
		t.Fatal(err)
	}
	if guardados != 1 {
		t.Fatalf("o dia servido devia ficar guardado, há %d planos", guardados)
	}

	// Dia 20: a estratégia muda. Quem pergunta pelo dia 19 tem de receber o
	// dia 19 — não o dia 19 refeito com o alvo novo.
	svc, _, _, _ = baseNutricaoMesmaBase(t, pool, time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC))
	estrategia(t, pool, ctx, userID, 2600, "2026-09-20")

	relido, err := svc.Today(ctx, pedido(userID, ontem))
	if err != nil {
		t.Fatalf("reler o dia 19: %v", err)
	}
	if relido.Day.Kcal != 2100 {
		t.Errorf("o dia 19 passou a valer %d kcal — foi remontado com a estratégia de hoje", relido.Day.Kcal)
	}
	if len(relido.Day.Meals) != len(vivido.Day.Meals) {
		t.Fatalf("%d refeições ao reler, %d quando foi vivido",
			len(relido.Day.Meals), len(vivido.Day.Meals))
	}
	for i, m := range vivido.Day.Meals {
		if relido.Day.Meals[i].Kcal != m.Kcal || len(relido.Day.Meals[i].Items) != len(m.Items) {
			t.Errorf("refeição %s mudou: %d kcal/%d itens ≠ %d kcal/%d itens",
				m.Slot, relido.Day.Meals[i].Kcal, len(relido.Day.Meals[i].Items), m.Kcal, len(m.Items))
		}
	}

	// E hoje continua a acompanhar a estratégia nova: guardar o passado não
	// pode congelar o presente.
	hoje, err := svc.Today(ctx, pedido(userID, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if hoje.Day.Kcal != 2600 {
		t.Errorf("hoje devia valer 2600 kcal, veio %d", hoje.Day.Kcal)
	}
}

/*
 * Um dia passado que nunca foi guardado não passa a estar.
 *
 * É o caso de todos os dias anteriores a isto existir. Remontá-los para os
 * escrever seria inventar história: ficaria gravado como "o plano daquele dia"
 * um plano que naquele dia ninguém viu.
 */
func TestDiaPassadoSemRegistoNaoSeInventa(t *testing.T) {
	svc, pool, ctx, userID := baseNutricao(t, time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC))
	estrategia(t, pool, ctx, userID, 2100, "2026-09-01")

	if _, err := svc.Today(ctx, pedido(userID, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}

	var guardados int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM daily_plan`).Scan(&guardados); err != nil {
		t.Fatal(err)
	}
	if guardados != 0 {
		t.Fatalf("um dia passado sem registo não devia ser escrito, há %d planos", guardados)
	}
}

// baseNutricaoMesmaBase monta outro serviço sobre a base já criada, para se
// poder mudar o relógio a meio — que é o que um dia a passar faz.
func baseNutricaoMesmaBase(t *testing.T, pool *pgxpool.Pool, agora time.Time) (*service.NutritionService, *pgxpool.Pool, context.Context, string) {
	t.Helper()
	tx := repo.NewTxManager(pool)
	svc := service.NewNutritionService(
		repo.NewGoalRepo(tx), repo.NewMealPrefRepo(tx),
		nutrition.DefaultConfig(), goal.DefaultConfig(),
	).ComPlanoGuardado(repo.NewNutritionPlanRepo(tx), clock.NewFixed(agora))
	return svc, pool, context.Background(), ""
}
