package postgres_test

import (
	"context"
	"os"
	"testing"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	"github.com/airosp/airo-api/migrations"
	"log/slog"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Stop()
	os.Exit(code)
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// T1.1 — o DDL da especificação aplica-se limpo a um Postgres real.
//
// Até aqui a migração só tinha sido **lida**. Ler não prova nada sobre SQL: um
// tipo que não existe, uma chave estrangeira para uma tabela declarada mais
// abaixo ou um `CHECK` mal escrito só aparecem ao aplicar.
func TestMigrationAppliesCleanly(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()

	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatalf("aplicar: %v", err)
	}

	var tables, types int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		 WHERE n.nspname = 'public' AND t.typtype = 'e'`).Scan(&types); err != nil {
		t.Fatal(err)
	}
	// Não se fixa o número de tabelas: cresce a cada migração, e um teste que
	// falha por isso falha pela razão errada. O que interessa é que as tabelas
	// que o domínio precisa existem, e que os tipos enumerados estão lá.
	for _, table := range []string{
		"app_user", "profile", "measurement",
		"goal", "journey", "phase", "cycle", "target", "plan",
		"exercise", "workout_session", "exercise_prescription", "exercise_set",
		"food", "nutrition_strategy", "daily_plan", "planned_meal", "nutrition_log",
		// `risk` saiu na 0026: os riscos vivem inteiros em
		// `assessment.snapshot`, e parti-los em duas metades era ter de as
		// manter de acordo.
		"assessment", "adaptation", "journey_event",
		"otp_challenge", "device", "refresh_token", "auth_event",
		"schema_migration",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("tabela %q em falta", table)
		}
	}
	if tables < 30 {
		t.Errorf("%d tabelas — o esquema encolheu", tables)
	}
	// E a 0002 acrescentou as colunas de idempotência.
	for _, table := range []string{"workout_session", "nutrition_log"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			                WHERE table_name = $1 AND column_name = 'idempotency_key')`,
			table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("%s sem idempotency_key", table)
		}
	}
	// Também não se fixa o número de tipos, pela mesma razão: cresce com cada
	// migração. O que interessa é que os que o domínio usa existem.
	for _, enum := range []string{
		"goal_horizon", "goal_direction", "lifecycle",
		"session_focus", "session_role", "session_status",
		"meal_slot", "log_status", "otp_channel", "otp_status",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_type WHERE typname = $1 AND typtype = 'e')`,
			enum).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("tipo %q em falta", enum)
		}
	}
	if types < 33 {
		t.Errorf("%d tipos — o esquema encolheu", types)
	}
	t.Logf("esquema aplicado: %d tabelas, %d tipos enumerados", tables, types)
}

// Aplicar duas vezes não faz nada da segunda: é o que torna seguro correr o
// passo de migração em cada deploy.
func TestMigrationIsIdempotent(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, _ := airopg.Load(migrations.FS)

	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatalf("segunda aplicação: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migration`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(migs) {
		t.Fatalf("%d migrações registadas, aplicadas %d", n, len(migs))
	}
	if pending, err := airopg.Pending(ctx, pool, migs); err != nil || len(pending) != 0 {
		t.Fatalf("pendentes depois de aplicar: %v %v", pending, err)
	}
}

// T0.7 — `up` seguido de `down` devolve a base ao estado inicial.
func TestMigrationDownRemovesEverything(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, _ := airopg.Load(migrations.FS)

	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatal(err)
	}
	// Uma de cada vez, de propósito: desfazer tudo de uma vez é um gesto que
	// ninguém quer ter feito por engano. Aqui percorre-se até ao fim.
	for i := 0; i <= len(migs); i++ {
		if err := airopg.Down(ctx, pool, migs, quietLog()); err != nil {
			t.Fatalf("desfazer: %v", err)
		}
	}

	var tables int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name <> 'schema_migration'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("%d tabelas sobraram depois do down", tables)
	}
	// E volta a poder ser aplicada.
	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatalf("reaplicar depois do down: %v", err)
	}
}

// Editar uma migração já aplicada tem de ser recusado: é o caso silencioso em
// que o esquema diverge entre ambientes sem nada a dizê-lo.
func TestEditedMigrationIsRefused(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, _ := airopg.Load(migrations.FS)

	if err := airopg.Up(ctx, pool, migs, quietLog()); err != nil {
		t.Fatal(err)
	}

	tampered := make([]airopg.Migration, len(migs))
	copy(tampered, migs)
	tampered[0].Checksum = "outra-coisa-qualquer"

	err := airopg.Up(ctx, pool, tampered, quietLog())
	if err == nil {
		t.Fatal("uma migração alterada depois de aplicada devia ser recusada")
	}
	t.Logf("recusada, como devia: %v", err)
}
