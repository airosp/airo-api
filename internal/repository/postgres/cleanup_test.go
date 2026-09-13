package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/migrations"
)

// A limpeza apaga o que já não vale, e **não** apaga o que ainda vale.
//
// A segunda metade é a que importa: um worker que apaga de mais põe pessoas
// fora da app sem razão, e ninguém percebe porquê.
func TestLimpezaApagaOQueExpirouENaoOResto(t *testing.T) {
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

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841110000','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	// Um desafio expirado há muito, e um ainda válido.
	// `expires_at > created_at` é uma restrição do esquema, e é boa: um desafio
	// que nasce expirado não é um desafio. Recua-se a criação também.
	if _, err := pool.Exec(ctx,
		`INSERT INTO otp_challenge (phone_e164, code_hash, channel, created_at, expires_at, status)
		 VALUES ('+258841110000', '\x00'::bytea, 'whatsapp',
		         now() - interval '4 hours', now() - interval '3 hours', 'expired')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO otp_challenge (phone_e164, code_hash, channel, expires_at, status)
		 VALUES ('+258841110001', '\x00'::bytea, 'whatsapp', now() + interval '5 minutes', 'pending')`); err != nil {
		t.Fatal(err)
	}

	// Os tokens pertencem a um aparelho, e o aparelho tem de existir.
	for _, d := range []string{"aparelho-1", "aparelho-2"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO device (id, user_id, platform) VALUES ($1, $2, 'web')`, d, userID); err != nil {
			t.Fatal(err)
		}
	}

	// Um token vivo e um revogado há muito.
	if _, err := pool.Exec(ctx,
		`INSERT INTO refresh_token (user_id, family_id, device_id, token_hash, expires_at)
		 VALUES ($1, gen_random_uuid(), 'aparelho-1', '\x01'::bytea, now() + interval '30 days')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO refresh_token (user_id, family_id, device_id, token_hash, expires_at, revoked_at)
		 VALUES ($1, gen_random_uuid(), 'aparelho-2', '\x02'::bytea, now() - interval '60 days', now() - interval '60 days')`,
		userID); err != nil {
		t.Fatal(err)
	}

	accounts := repo.NewAccountRepo(repo.NewTxManager(pool))
	out, err := accounts.Cleanup(ctx, "1 hour", "30 days", "90 days")
	if err != nil {
		t.Fatal(err)
	}

	conta := func(q string) int {
		var n int
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if out.Challenges < 1 {
		t.Errorf("nenhum desafio expirado foi apagado")
	}
	if n := conta(`SELECT count(*) FROM otp_challenge WHERE status = 'pending'`); n != 1 {
		t.Errorf("o desafio ainda válido devia ficar, ficaram %d", n)
	}
	if out.Tokens != 1 {
		t.Errorf("esperava 1 token apagado, foram %d", out.Tokens)
	}
	if n := conta(`SELECT count(*) FROM refresh_token WHERE revoked_at IS NULL`); n != 1 {
		t.Errorf("o token vivo devia ficar, ficaram %d", n)
	}
}
