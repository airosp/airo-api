//go:build devtools

// authcheck resume os últimos acontecimentos de autenticação.
//
// Existe porque "recebi o código" não diz se a sessão chegou a abrir: entre a
// mensagem e a sessão há o verify, o utilizador a nascer e o refresh a ser
// emitido, e qualquer um deles pode falhar em silêncio.
//
// Não mostra números nem códigos — só o tipo de acontecimento e a hora.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	url := os.Getenv("AIRO_DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "falta AIRO_DATABASE_URL")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx,
		`SELECT kind, occurred_at FROM auth_event ORDER BY occurred_at DESC LIMIT 12`)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer rows.Close()
	fmt.Println("últimos acontecimentos:")
	for rows.Next() {
		var kind string
		var at time.Time
		if err := rows.Scan(&kind, &at); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("  %-22s %s\n", kind, at.Format("15:04:05"))
	}

	var users, devices, tokens, profiles int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM app_user`).Scan(&users)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM device`).Scan(&devices)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_token WHERE revoked_at IS NULL`).Scan(&tokens)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM profile`).Scan(&profiles)
	fmt.Printf("\ncontas=%d aparelhos=%d sessões_activas=%d perfis=%d\n",
		users, devices, tokens, profiles)
}
