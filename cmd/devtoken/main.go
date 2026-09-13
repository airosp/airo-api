//go:build devtools

// devtoken assina um access token para um utilizador que já existe.
//
// Existe para se poder exercitar as rotas privadas contra um ambiente a sério
// sem receber um código por WhatsApp. Não contorna nada: usa o mesmo segredo
// que o servidor usa para verificar, e quem o tem já é quem manda no servidor.
//
//	AIRO_JWT_SECRET=… AIRO_DATABASE_URL=… go run -tags devtools ./cmd/devtoken [telefone]
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	secret := os.Getenv("AIRO_JWT_SECRET")
	dbURL := os.Getenv("AIRO_DATABASE_URL")
	if secret == "" || dbURL == "" {
		fmt.Fprintln(os.Stderr, "faltam AIRO_JWT_SECRET e AIRO_DATABASE_URL")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()

	var userID, phone string
	query := `SELECT u.id, u.phone_e164 FROM app_user u
	          JOIN profile p ON p.user_id = u.id
	          ORDER BY p.updated_at DESC LIMIT 1`
	args := []any{}
	if len(os.Args) > 1 {
		query = `SELECT id, phone_e164 FROM app_user WHERE phone_e164 = $1`
		args = append(args, os.Args[1])
	}
	if err := pool.QueryRow(ctx, query, args...).Scan(&userID, &phone); err != nil {
		fmt.Fprintln(os.Stderr, "utilizador:", err)
		os.Exit(1)
	}

	token, expires, err := auth.NewTokenIssuer([]byte(secret), time.Now).Issue(userID, "devtoken")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// O número vai mascarado: o token já é o que dá acesso, e o telefone não
	// precisa de estar no histórico do terminal.
	fmt.Fprintf(os.Stderr, "utilizador %s (%s), válido até %s\n",
		userID, auth.MaskPhone(phone), expires.Format("15:04:05"))
	fmt.Println(token)
}
