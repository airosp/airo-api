package postgres_test

import (
	"context"
	"strings"
	"testing"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func migrated(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatal(err)
	}
	return pool, ctx
}

// Cria um utilizador e um objectivo, e devolve os ids. O esquema tem cadeias de
// chaves estrangeiras: sem isto, cada teste repetia vinte linhas de preparação.
func seedGoal(t *testing.T, pool *pgxpool.Pool, ctx context.Context, horizon string) (userID, goalID string) {
	t.Helper()
	var phone string
	if err := pool.QueryRow(ctx, `SELECT '+2588412' || lpad((floor(random()*100000))::int::text, 5, '0')`).Scan(&phone); err != nil {
		t.Fatal(err)
	}
	err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ($1, 'MZ') RETURNING id`, phone,
	).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO goal (user_id, type, horizon, direction, priority, status)
		 VALUES ($1, 'outcome', $2, 'lose_weight', 'weight', 'active') RETURNING id`,
		userID, horizon).Scan(&goalID)
	if err != nil {
		t.Fatal(err)
	}
	return userID, goalID
}

// insertSession preenche o que o esquema exige. Descobri-o a tentar: uma sessão
// precisa de título, foco e estado, e não só de tempos.
func insertSession(ctx context.Context, pool *pgxpool.Pool, userID, status string, key *string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO workout_session
		   (user_id, title, focus, status, occurred_at, local_day, planned_seconds, duration_seconds, idempotency_key)
		 VALUES ($1, 'Corpo inteiro', 'full', $2, now(), CURRENT_DATE, 1200, 900, $3)`,
		userID, status, key)
	return err
}

// INVARIANTE 14 — horizonte aberto ⇒ `target_date IS NULL`.
//
// Não se inventa uma data artificial para uniformizar o modelo: confundir
// "perder 5 kg em 90 dias" com "treinar para sempre" é o erro que a
// especificação rejeita explicitamente. A garantia é um `CHECK`, e um `CHECK`
// só se prova a tentar violá-lo.
func TestInvariant14_OpenEndedHasNoTargetDate(t *testing.T) {
	pool, ctx := migrated(t)

	for _, horizon := range []string{"open_ended", "review_based"} {
		_, goalID := seedGoal(t, pool, ctx, horizon)

		// Com data: tem de ser recusado pela base de dados.
		_, err := pool.Exec(ctx,
			`INSERT INTO journey (goal_id, horizon, start_date, target_date, cycle_weeks)
			 VALUES ($1, $2, CURRENT_DATE, CURRENT_DATE + 90, 4)`,
			goalID, horizon)
		if err == nil {
			t.Fatalf("%s: uma jornada sem prazo aceitou uma data-alvo", horizon)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "check") &&
			!strings.Contains(strings.ToLower(err.Error()), "constraint") {
			t.Fatalf("%s: recusado, mas não por um CHECK: %v", horizon, err)
		}

		// Sem data: tem de passar.
		if _, err := pool.Exec(ctx,
			`INSERT INTO journey (goal_id, horizon, start_date, target_date, cycle_weeks)
			 VALUES ($1, $2, CURRENT_DATE, NULL, 4)`,
			goalID, horizon); err != nil {
			t.Fatalf("%s sem data devia ser aceite: %v", horizon, err)
		}
		t.Logf("%s: data-alvo recusada pelo esquema, NULL aceite", horizon)
	}

	// E o horizonte fechado **exige** a data.
	_, goalID := seedGoal(t, pool, ctx, "fixed")
	if _, err := pool.Exec(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, target_date) VALUES ($1, 'fixed', CURRENT_DATE, NULL)`,
		goalID); err == nil {
		t.Fatal("horizonte fixo sem data-alvo devia ser recusado")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, target_date) VALUES ($1, 'fixed', CURRENT_DATE, CURRENT_DATE + 90)`,
		goalID); err != nil {
		t.Fatalf("horizonte fixo com data devia ser aceite: %v", err)
	}
	// E um ciclo num horizonte fechado também é recusado: as duas formas não se
	// misturam.
	if _, err := pool.Exec(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, target_date, cycle_weeks)
		 VALUES ($1, 'fixed', CURRENT_DATE, CURRENT_DATE + 90, 4)`,
		goalID); err == nil {
		t.Fatal("horizonte fixo com cycle_weeks devia ser recusado")
	}
}

// INVARIANTE 7 — uma sessão grava **sempre**, feita ou saltada.
//
// A adesão precisa de distinguir quem abriu e desistiu de quem nunca apareceu, e
// o registo saltado é a única prova da segunda. O esquema tem de aceitar os dois
// estados na mesma tabela — não um "histórico" e uma "lixeira".
func TestInvariant07_SkippedSessionsAreRecordedToo(t *testing.T) {
	pool, ctx := migrated(t)
	userID, _ := seedGoal(t, pool, ctx, "fixed")

	for _, status := range []string{"completed", "skipped"} {
		if err := insertSession(ctx, pool, userID, status, nil); err != nil {
			t.Fatalf("gravar sessão %q: %v", status, err)
		}
	}

	var completed, skipped int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'completed'),
		        count(*) FILTER (WHERE status = 'skipped')
		 FROM workout_session WHERE user_id = $1`, userID).Scan(&completed, &skipped); err != nil {
		t.Fatal(err)
	}
	if completed != 1 || skipped != 1 {
		t.Fatalf("%d concluídas e %d saltadas — as duas têm de ficar", completed, skipped)
	}
	t.Log("as duas gravam na mesma tabela, como a adesão exige")
}

// A idempotência é uma garantia do esquema, não do código: o cliente treina
// offline e sincroniza — possivelmente duas vezes.
func TestIdempotencyKeyIsUniquePerUser(t *testing.T) {
	pool, ctx := migrated(t)
	userID, _ := seedGoal(t, pool, ctx, "fixed")

	key := "11111111-1111-4111-8111-111111111111"
	if err := insertSession(ctx, pool, userID, "completed", &key); err != nil {
		t.Fatalf("primeira gravação: %v", err)
	}
	if err := insertSession(ctx, pool, userID, "completed", &key); err == nil {
		t.Fatal("a mesma chave idempotente criou um segundo registo")
	}

	// Mas duas sessões **sem** chave continuam a poder existir: quem treina duas
	// vezes no mesmo dia não está a duplicar nada.
	for i := 0; i < 2; i++ {
		if err := insertSession(ctx, pool, userID, "completed", nil); err != nil {
			t.Fatalf("sessão sem chave %d: %v", i, err)
		}
	}

	// E a mesma chave noutro utilizador é outra sessão: dois telemóveis podem
	// gerar o mesmo uuid sem que isso queira dizer o mesmo treino.
	other, _ := seedGoal(t, pool, ctx, "fixed")
	if err := insertSession(ctx, pool, other, "completed", &key); err != nil {
		t.Fatalf("a mesma chave noutro utilizador devia passar: %v", err)
	}
}

// Um objectivo activo por utilizador. Dois ao mesmo tempo é o que impede a
// jornada de ter um "porquê".
func TestOnlyOneActiveGoalPerUser(t *testing.T) {
	pool, ctx := migrated(t)
	userID, _ := seedGoal(t, pool, ctx, "fixed")

	_, err := pool.Exec(ctx,
		`INSERT INTO goal (user_id, type, horizon, direction, priority, status)
		 VALUES ($1, 'outcome', 'fixed', 'gain_weight', 'muscle', 'active')`, userID)
	if err == nil {
		t.Fatal("um segundo objectivo activo devia ser recusado")
	}

	// Mas um arquivado não conflitua.
	if _, err := pool.Exec(ctx,
		`INSERT INTO goal (user_id, type, horizon, direction, priority, status)
		 VALUES ($1, 'outcome', 'fixed', 'gain_weight', 'muscle', 'archived')`, userID); err != nil {
		t.Fatalf("objectivo arquivado devia ser aceite: %v", err)
	}
}

// Apagar a conta apaga tudo — RNF-11, o direito ao esquecimento.
func TestDeletingUserCascades(t *testing.T) {
	pool, ctx := migrated(t)
	userID, goalID := seedGoal(t, pool, ctx, "fixed")

	if _, err := pool.Exec(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, target_date) VALUES ($1, 'fixed', CURRENT_DATE, CURRENT_DATE + 90)`,
		goalID); err != nil {
		t.Fatal(err)
	}
	if err := insertSession(ctx, pool, userID, "completed", nil); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, userID); err != nil {
		t.Fatalf("apagar conta: %v", err)
	}

	for _, table := range []string{"goal", "journey", "workout_session"} {
		var n int
		q := "SELECT count(*) FROM " + table
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s: %d linhas sobraram depois de apagar a conta", table, n)
		}
	}
}
