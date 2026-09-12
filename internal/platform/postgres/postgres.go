// Package postgres dá o pool de ligações e o executor de migrações.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open devolve um pool pronto, já com uma ligação verificada.
//
// Verificar no arranque é deliberado: uma API que sobe com a base de dados
// inacessível responde 200 no /healthz e falha em todos os pedidos reais.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("URL de base de dados inválido: %w", err)
	}

	// Um pool pequeno e com reciclagem: o Postgres do EasyPanel é partilhado, e
	// ligações que vivem para sempre acumulam estado de sessão.
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("base de dados inacessível: %w", err)
	}
	return pool, nil
}
