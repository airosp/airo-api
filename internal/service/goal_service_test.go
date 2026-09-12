package service_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Stop()
	os.Exit(code)
}

func setup(t *testing.T) (*service.GoalService, *pgxpool.Pool, string) {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()

	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	svc := service.NewGoalService(tx, repo.NewGoalRepo(tx), service.Configs{
		Goal:      goal.DefaultConfig(),
		Journey:   journey.DefaultConfig(),
		Nutrition: nutrition.DefaultConfig(),
	}, clock.NewFixed(time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)))

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841234567','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return svc, pool, userID
}

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }
func s(v string) *string   { return &v }

func fixedInput(userID string) service.CreateGoalInput {
	target := time.Date(2026, 12, 29, 0, 0, 0, 0, time.UTC)
	return service.CreateGoalInput{
		UserID: userID, Type: "outcome", Horizon: "fixed",
		Direction: "lose_weight", Priority: "weight",
		StartDate: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), TargetDate: &target,
		CurrentWeightKg: 80, TargetWeightKg: f(74), HeightCm: f(175), Age: i(34), Sex: s("male"),
		DaysPerWeek: 3, SessionMinutes: 45, Experience: "intermediate",
		NutritionGoal: "lose_fat", MealsPerDay: 4,
	}
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// T4.3 — criar um objetivo escreve tudo, de uma vez.
func TestCreateGoalWritesEverything(t *testing.T) {
	svc, pool, userID := setup(t)

	out, err := svc.Create(context.Background(), fixedInput(userID))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		table string
		want  int
	}{
		{"goal", 1}, {"journey", 1}, {"phase", 4}, {"target", 1},
		{"plan", 1}, {"nutrition_strategy", 1}, {"assessment", 1}, {"journey_event", 3},
	} {
		if got := count(t, pool, c.table); got != c.want {
			t.Errorf("%s: %d linhas, esperava %d", c.table, got, c.want)
		}
	}

	if out.GoalID == "" || out.JourneyID == "" || out.PlanID == "" {
		t.Fatalf("ids em falta: %+v", out)
	}
	if len(out.Phases) != 4 {
		t.Fatalf("%d fases", len(out.Phases))
	}
	// A primeira fase é adaptação, e o plano dela é mais leve do que o pedido.
	var freq, minutes int
	var intensity string
	if err := pool.QueryRow(context.Background(),
		`SELECT frequency_per_week, session_minutes, intensity FROM plan`).Scan(&freq, &minutes, &intensity); err != nil {
		t.Fatal(err)
	}
	if freq != 2 || intensity != "low" {
		t.Errorf("adaptação devia baixar a frequência e a intensidade: %dx/semana, %s", freq, intensity)
	}
	if minutes >= 45 {
		t.Errorf("adaptação devia encurtar as sessões: %d min", minutes)
	}
	t.Logf("plano da fase de adaptação: %dx/semana, %d min, intensidade %s", freq, minutes, intensity)
}

// INVARIANTE: o piso calórico é absoluto, e o esquema também o garante.
func TestStrategyRespectsCalorieFloor(t *testing.T) {
	svc, pool, userID := setup(t)
	in := fixedInput(userID)
	in.CurrentWeightKg = 48
	in.TargetWeightKg = f(45)
	in.HeightCm = f(150)
	in.Age = i(60)

	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	var target int
	if err := pool.QueryRow(context.Background(),
		`SELECT calorie_target FROM nutrition_strategy`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if target < 1500 {
		t.Fatalf("alvo calórico %d abaixo do piso", target)
	}
}

// Horizonte aberto abre um ciclo em vez de fases — e sem data-alvo.
func TestOpenEndedOpensACycle(t *testing.T) {
	svc, pool, userID := setup(t)
	in := fixedInput(userID)
	in.Horizon = "open_ended"
	in.TargetDate = nil

	out, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.CycleID == nil {
		t.Fatal("horizonte aberto devia abrir um ciclo")
	}
	if n := count(t, pool, "phase"); n != 0 {
		t.Fatalf("%d fases num horizonte aberto — não há transição para lado nenhum", n)
	}
	if n := count(t, pool, "cycle"); n != 1 {
		t.Fatalf("%d ciclos", n)
	}

	var cycleWeeks *int
	var due *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT j.cycle_weeks, t.due_date FROM journey j LEFT JOIN target t ON t.journey_id = j.id`,
	).Scan(&cycleWeeks, &due); err != nil {
		t.Fatal(err)
	}
	if cycleWeeks == nil || *cycleWeeks != 4 {
		t.Fatalf("ciclo de %v semanas", cycleWeeks)
	}
	// O alvo existe; a data não. É a distinção que a especificação insiste em
	// preservar.
	if due != nil {
		t.Fatalf("o alvo de um horizonte aberto não tem data: %v", due)
	}
}

// O horizonte e a data têm de concordar — e o erro é explicável.
func TestHorizonMismatchIsRefused(t *testing.T) {
	svc, pool, userID := setup(t)

	open := fixedInput(userID)
	open.Horizon = "open_ended" // com data-alvo
	if _, err := svc.Create(context.Background(), open); !errors.Is(err, service.ErrHorizonMismatch) {
		t.Fatalf("esperava ErrHorizonMismatch, deu %v", err)
	}

	fixed := fixedInput(userID)
	fixed.TargetDate = nil // sem data
	if _, err := svc.Create(context.Background(), fixed); !errors.Is(err, service.ErrHorizonMismatch) {
		t.Fatalf("esperava ErrHorizonMismatch, deu %v", err)
	}

	if n := count(t, pool, "goal"); n != 0 {
		t.Fatalf("%d objetivos gravados apesar do erro", n)
	}
}

// Um objetivo activo de cada vez. O conflito vem do índice do esquema, não de
// uma consulta prévia: verificar antes e inserir depois é uma corrida que duas
// gravações simultâneas ganham as duas.
func TestSecondActiveGoalIsRefused(t *testing.T) {
	svc, pool, userID := setup(t)

	if _, err := svc.Create(context.Background(), fixedInput(userID)); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), fixedInput(userID))
	if !errors.Is(err, repo.ErrGoalAlreadyActive) {
		t.Fatalf("esperava ErrGoalAlreadyActive, deu %v", err)
	}

	// E o segundo não deixou nada para trás.
	for _, table := range []string{"goal", "journey", "plan", "nutrition_strategy"} {
		if n := count(t, pool, table); n != 1 {
			t.Fatalf("%s: %d linhas depois do segundo pedido falhar", table, n)
		}
	}
}

// **Ou entra tudo, ou nada.**
//
// Um objetivo sem jornada, ou uma jornada sem plano, é pior do que nenhum
// objetivo: a app abriria num estado que nenhum ecrã sabe desenhar. Provo-o a
// forçar a falha no último passo.
func TestCreateIsAllOrNothing(t *testing.T) {
	svc, pool, userID := setup(t)
	ctx := context.Background()

	// A avaliação é o último INSERT da transacção. Tirar-lhe a tabela faz o
	// passo falhar sem tocar em nada do que veio antes.
	if _, err := pool.Exec(ctx, `ALTER TABLE assessment RENAME TO assessment_escondida`); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Create(ctx, fixedInput(userID)); err == nil {
		t.Fatal("esperava falha no último passo")
	}

	for _, table := range []string{"goal", "journey", "phase", "target", "plan", "nutrition_strategy", "journey_event"} {
		if n := count(t, pool, table); n != 0 {
			t.Fatalf("%s: %d linhas sobreviveram a uma transacção que falhou", table, n)
		}
	}
	t.Log("nada ficou escrito, como devia")
}

// Avaliar não grava. É o que permite correr o motor a cada movimento da régua.
func TestAssessDoesNotWrite(t *testing.T) {
	svc, pool, userID := setup(t)

	a := svc.Assess(fixedInput(userID))
	if a.Status == "" {
		t.Fatal("avaliação vazia")
	}
	for _, table := range []string{"goal", "journey", "assessment"} {
		if n := count(t, pool, table); n != 0 {
			t.Fatalf("%s: avaliar gravou %d linhas", table, n)
		}
	}
}

// A avaliação guarda a versão da configuração: sem ela, uma avaliação de há três
// meses deixa de ser explicável quando os limiares mudarem.
func TestAssessmentStoresConfigVersion(t *testing.T) {
	svc, pool, userID := setup(t)
	if _, err := svc.Create(context.Background(), fixedInput(userID)); err != nil {
		t.Fatal(err)
	}
	var version string
	if err := pool.QueryRow(context.Background(),
		`SELECT snapshot->>'configVersion' FROM assessment`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version == "" || version != goal.DefaultConfig().Version {
		t.Fatalf("versão da configuração guardada: %q", version)
	}
}
