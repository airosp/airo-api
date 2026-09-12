package http

import (
	"context"

	"github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaState responde a uma pergunta só: o esquema está em dia?
//
// Vive no transporte porque é o /readyz que a faz. Consulta a cada chamada e não
// no arranque: as migrações correm noutro processo, e um valor lido uma vez
// ficaria a dizer "atrasado" para sempre depois de já estar em dia.
type SchemaState struct {
	Migrations []postgres.Migration
	Pool       *pgxpool.Pool
}

func (s SchemaState) PendingCount(ctx context.Context) (int, error) {
	if s.Pool == nil {
		return 0, nil
	}
	pending, err := postgres.Pending(ctx, s.Pool, s.Migrations)
	if err != nil {
		return 0, err
	}
	return len(pending), nil
}
