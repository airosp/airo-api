package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockID é arbitrário mas fixo: duas instâncias a arrancar ao mesmo tempo num
// deploy pegam no mesmo cadeado, e a segunda espera em vez de aplicar o mesmo
// DDL em paralelo.
const lockID int64 = 0x4149524f // "AIRO"

type Migration struct {
	Version  int
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

// Load lê os pares NNNN_nome.up.sql / .down.sql de um sistema de ficheiros.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.Glob(fsys, "*.up.sql")
	if err != nil {
		return nil, err
	}
	out := make([]Migration, 0, len(entries))
	for _, up := range entries {
		base := strings.TrimSuffix(up, ".up.sql")
		parts := strings.SplitN(base, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("nome de migração inválido: %q (esperado NNNN_nome.up.sql)", up)
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("versão inválida em %q: %w", up, err)
		}
		upSQL, err := fs.ReadFile(fsys, up)
		if err != nil {
			return nil, err
		}
		// O .down é obrigatório: uma migração que não se desfaz é uma decisão
		// irreversível tomada sem se dar por isso.
		downSQL, err := fs.ReadFile(fsys, base+".down.sql")
		if err != nil {
			return nil, fmt.Errorf("falta o .down.sql de %q: %w", base, err)
		}
		sum := sha256.Sum256(upSQL)
		out = append(out, Migration{
			Version:  version,
			Name:     parts[1],
			UpSQL:    string(upSQL),
			DownSQL:  string(downSQL),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })

	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duas migrações com a versão %d", out[i].Version)
		}
	}
	return out, nil
}

const schemaTable = `
CREATE TABLE IF NOT EXISTS schema_migration (
    version     integer PRIMARY KEY,
    name        text NOT NULL,
    checksum    text NOT NULL,
    applied_at  timestamptz NOT NULL DEFAULT now(),
    duration_ms integer NOT NULL
)`

// Up aplica as migrações em falta, por ordem, uma transação cada.
//
// Uma transação por migração e não uma para todas: se a quinta falhar, as
// quatro primeiras ficam aplicadas e registadas, e a correcção é a quinta —
// não voltar ao princípio.
func Up(ctx context.Context, pool *pgxpool.Pool, migs []Migration, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return fmt.Errorf("cadeado: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID) }()

	if _, err := conn.Exec(ctx, schemaTable); err != nil {
		return fmt.Errorf("tabela de migrações: %w", err)
	}

	applied := map[int]string{}
	rows, err := conn.Query(ctx, "SELECT version, checksum FROM schema_migration")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return err
		}
		applied[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range migs {
		if sum, ok := applied[m.Version]; ok {
			// Editar uma migração já aplicada faz o esquema divergir entre
			// ambientes sem nada a dizê-lo. Isto diz.
			if sum != m.Checksum {
				return fmt.Errorf("migração %04d_%s foi alterada depois de aplicada (checksum diferente); cria uma nova em vez de editar esta", m.Version, m.Name)
			}
			continue
		}

		start := time.Now()
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO schema_migration (version, name, checksum, duration_ms) VALUES ($1,$2,$3,$4)`,
				m.Version, m.Name, m.Checksum, time.Since(start).Milliseconds())
			return err
		})
		if err != nil {
			return fmt.Errorf("migração %04d_%s: %w", m.Version, m.Name, err)
		}
		log.Info("migração aplicada", "version", m.Version, "name", m.Name, "ms", time.Since(start).Milliseconds())
	}
	return nil
}

// Down desfaz a última migração aplicada. Uma de cada vez, de propósito.
func Down(ctx context.Context, pool *pgxpool.Pool, migs []Migration, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID) }()

	var version int
	err = conn.QueryRow(ctx, "SELECT version FROM schema_migration ORDER BY version DESC LIMIT 1").Scan(&version)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			log.Info("nada para desfazer")
			return nil
		}
		return err
	}

	for _, m := range migs {
		if m.Version != version {
			continue
		}
		return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.DownSQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "DELETE FROM schema_migration WHERE version = $1", version)
			if err == nil {
				log.Info("migração desfeita", "version", m.Version, "name", m.Name)
			}
			return err
		})
	}
	return fmt.Errorf("versão %d está aplicada mas não existe nos ficheiros", version)
}

// Pending devolve as migrações por aplicar, sem aplicar nenhuma.
//
// Serve ao arranque: uma API que serve com o esquema atrasado responde 200 e
// falha em consultas a colunas que ainda não existem — o pior dos dois mundos,
// porque parece estar de pé.
func Pending(ctx context.Context, pool *pgxpool.Pool, migs []Migration) ([]Migration, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migration')`,
	).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return migs, nil
	}

	applied := map[int]bool{}
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migration")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Migration
	for _, m := range migs {
		if !applied[m.Version] {
			out = append(out, m)
		}
	}
	return out, nil
}
