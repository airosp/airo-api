package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func trainingSetup(t *testing.T) (*service.TrainingService, *pgxpool.Pool, string) {
	t.Helper()
	_, pool, userID := setup(t)
	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)

	n, err := catalog.SeedExercises(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("catálogo vazio")
	}

	svc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog), training.DefaultConfig(),
		clock.NewFixed(time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)))
	return svc, pool, userID
}

func todayInput() service.TodayInput {
	return service.TodayInput{
		PlanLabel: "Full Body", Experience: "intermediate",
		Equipment: []string{"dumbbells"}, WorkoutMinutes: 45,
		LocalDay: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
	}
}

// T1.9 — o catálogo carrega, e carregar duas vezes actualiza em vez de duplicar.
func TestSeedCatalogIsIdempotent(t *testing.T) {
	_, pool, _ := setup(t)
	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	ctx := context.Background()

	n1, err := catalog.SeedExercises(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := catalog.SeedExercises(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n1 != n2 {
		t.Fatalf("%d e depois %d", n1, n2)
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exercise`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != n1 {
		t.Fatalf("%d linhas para %d exercícios — carregar duas vezes duplicou", rows, n1)
	}
	t.Logf("catálogo: %d exercícios, idempotente", rows)
}

// T4.7 — a sessão de hoje vem como pacote, não como lista de exercícios.
func TestTodayReturnsAPackage(t *testing.T) {
	svc, _, _ := trainingSetup(t)

	pkg, session, steps, err := svc.Today(todayInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Steps) != len(steps) {
		t.Fatalf("%d passos no pacote, %d na linha do tempo", len(pkg.Steps), len(steps))
	}
	if pkg.TotalSets == 0 || len(session.Exercises) == 0 {
		t.Fatalf("sessão vazia: %+v", pkg)
	}
	if pkg.CompletionThresholdSeconds <= 0 {
		t.Fatal("sem limiar de conclusão")
	}
	t.Logf("%s · %d exercícios · %d séries · %d passos", pkg.Title, len(session.Exercises), pkg.TotalSets, len(pkg.Steps))
}

// Um dia de recuperação usa o orçamento do dia, não o do perfil.
func TestRecoveryDayUsesItsOwnBudget(t *testing.T) {
	svc, _, _ := trainingSetup(t)

	in := todayInput()
	in.PlanLabel = "Recovery"
	pkg, _, _, err := svc.Today(in)
	if err != nil {
		t.Fatal(err)
	}
	// 20 min, não 45. Com ±6% de convergência.
	minutes := pkg.EstimatedSeconds / 60
	if minutes < 18 || minutes > 23 {
		t.Fatalf("dia de recuperação com %d min — devia ser ~20", minutes)
	}
}

func record(t *testing.T, svc *service.TrainingService, userID string, duration int, key string) service.RecordSessionResult {
	t.Helper()
	_, session, steps, err := svc.Today(todayInput())
	if err != nil {
		t.Fatal(err)
	}
	cfg := training.DefaultConfig()
	planned := training.RemainingSeconds(cfg, steps, 0)

	out, err := svc.Record(context.Background(), service.RecordSessionInput{
		UserID: userID, IdempotencyKey: key,
		Title: session.Title, Focus: string(session.Focus),
		OccurredAt:      time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC),
		LocalDay:        time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
		PlannedSeconds:  planned,
		DurationSeconds: duration,
		SetsPlanned:     training.TotalSets(steps),
		SetsDone:        training.TotalSets(steps),
		Prescriptions:   service.PrescriptionsFrom(session),
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// **T4.9 — o servidor é que decide.**
//
// O corpo não traz `status`. Cinco segundos numa sessão de 45 minutos é uma
// sessão saltada, por muito que o cliente quisesse chamar-lhe outra coisa.
func TestServerDecidesWhetherTheSessionCounts(t *testing.T) {
	svc, _, userID := trainingSetup(t)

	_, _, steps, _ := svc.Today(todayInput())
	planned := training.RemainingSeconds(training.DefaultConfig(), steps, 0)

	skipped := record(t, svc, userID, 5, "")
	if skipped.Status != "skipped" {
		t.Fatalf("5 segundos deu %q", skipped.Status)
	}
	if skipped.CountsForStreak {
		t.Fatal("uma sessão saltada não conta para a sequência")
	}

	done := record(t, svc, userID, planned, "")
	if done.Status != "completed" {
		t.Fatalf("uma sessão inteira deu %q", done.Status)
	}
	if !done.CountsForStreak || done.Streak < 1 {
		t.Fatalf("devia contar: %+v", done)
	}
	t.Logf("5 s → %s · %d s → %s (sequência %d)", skipped.Status, planned, done.Status, done.Streak)
}

// O limiar é metade, e é generoso de propósito: quem treina depressa continua a
// contar.
func TestThresholdIsHalf(t *testing.T) {
	svc, _, userID := trainingSetup(t)
	_, _, steps, _ := svc.Today(todayInput())
	planned := training.RemainingSeconds(training.DefaultConfig(), steps, 0)

	justUnder := record(t, svc, userID, planned/2-10, "")
	if justUnder.Status != "skipped" {
		t.Errorf("abaixo de metade: %q", justUnder.Status)
	}
	justOver := record(t, svc, userID, planned/2+10, "")
	if justOver.Status != "completed" {
		t.Errorf("acima de metade: %q", justOver.Status)
	}
}

// INVARIANTE 7 — a sessão saltada grava na mesma, com as suas prescrições.
func TestSkippedSessionStillRecordsEverything(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	out := record(t, svc, userID, 5, "")
	ctx := context.Background()

	var status string
	var prescriptions, sets int
	if err := pool.QueryRow(ctx,
		`SELECT s.status,
		        (SELECT count(*) FROM exercise_prescription WHERE session_id = s.id),
		        (SELECT count(*) FROM exercise_set es
		          JOIN exercise_prescription p ON p.id = es.prescription_id
		         WHERE p.session_id = s.id)
		   FROM workout_session s WHERE s.id = $1`, out.ID).Scan(&status, &prescriptions, &sets); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" || prescriptions == 0 || sets == 0 {
		t.Fatalf("saltada mas incompleta: %s, %d prescrições, %d séries", status, prescriptions, sets)
	}
	t.Logf("saltada e gravada: %d prescrições, %d séries", prescriptions, sets)
}

// Reenviar devolve o registo existente, não cria um segundo treino.
func TestRecordIsIdempotent(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	key := "33333333-3333-4333-8333-333333333333"

	first := record(t, svc, userID, 2000, key)
	second := record(t, svc, userID, 2000, key)

	if second.ID != first.ID {
		t.Fatalf("ids diferentes: %s ≠ %s", second.ID, first.ID)
	}
	if !second.Replayed {
		t.Error("o reenvio devia dizer que é um reenvio")
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workout_session`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d sessões — o reenvio criou outra", n)
	}
}

// A cópia do alvo para a série é deliberada: uma adaptação futura reescreve a
// prescrição, e o histórico tem de continuar a dizer o que foi pedido **naquele
// dia**.
func TestSetTargetIsCopiedNotReferenced(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	out := record(t, svc, userID, 2000, "")
	ctx := context.Background()

	var equal bool
	if err := pool.QueryRow(ctx,
		`SELECT bool_and(es.target = p.target)
		   FROM exercise_set es
		   JOIN exercise_prescription p ON p.id = es.prescription_id
		  WHERE p.session_id = $1`, out.ID).Scan(&equal); err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("o alvo da série devia ser cópia do da prescrição")
	}

	// E reescrever a prescrição não toca no histórico.
	if _, err := pool.Exec(ctx,
		`UPDATE exercise_prescription SET target = '{"type":"reps","reps":99}'::jsonb
		  WHERE session_id = $1`, out.ID); err != nil {
		t.Fatal(err)
	}
	var changed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM exercise_set es
		   JOIN exercise_prescription p ON p.id = es.prescription_id
		  WHERE p.session_id = $1 AND es.target->>'reps' = '99'`, out.ID).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatalf("%d séries mudaram com a prescrição — adaptar reescreveria o passado", changed)
	}
}

// A sequência conta dias seguidos com sessão **concluída**.
func TestStreakCountsOnlyCompletedDays(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	ctx := context.Background()

	// Três dias seguidos: concluída, saltada, concluída.
	days := []struct {
		day    string
		status string
	}{
		{"2026-09-10", "completed"},
		{"2026-09-11", "skipped"},
		{"2026-09-12", "completed"},
	}
	for _, d := range days {
		if _, err := pool.Exec(ctx,
			`INSERT INTO workout_session (user_id, title, focus, status, occurred_at, local_day,
			                              planned_seconds, duration_seconds)
			 VALUES ($1,'Corpo inteiro','full',$2,$3::date,$3::date,1200,1200)`,
			userID, d.status, d.day); err != nil {
			t.Fatal(err)
		}
	}

	out := record(t, svc, userID, 5, "") // hoje, saltada
	_ = out

	streak, err := repo.NewSessionRepo(repo.NewTxManager(pool), repo.NewCatalogRepo(repo.NewTxManager(pool))).
		Streak(ctx, userID, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// Hoje conta (concluída), ontem foi saltada: a sequência pára em 1.
	if streak != 1 {
		t.Fatalf("sequência %d — a saltada de ontem devia quebrá-la", streak)
	}
}

// Um exercício fora do catálogo é um erro de dados, não um caso a ignorar.
func TestUnknownExerciseIsRefused(t *testing.T) {
	svc, _, userID := trainingSetup(t)
	_, err := svc.Record(context.Background(), service.RecordSessionInput{
		UserID: userID, Title: "x", Focus: "full",
		OccurredAt: time.Now(), LocalDay: time.Now(),
		PlannedSeconds: 100, DurationSeconds: 100,
		Prescriptions: []repo.PrescriptionRow{{ExerciseSlug: "nao-existe", Position: 0, Role: "main", Sets: 1}},
	})
	if err == nil {
		t.Fatal("um exercício fora do catálogo devia ser recusado")
	}
	if errors.Is(err, repo.ErrDuplicateSession) {
		t.Fatal("erro trocado")
	}
}
