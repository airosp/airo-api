// Package postgres implementa os repositórios. É o único sítio com SQL.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier é o que os repositórios precisam: um pool ou uma transacção,
// indistintamente.
//
// É o que permite ao serviço decidir o limite da transacção sem que cada
// repositório saiba se está dentro de uma.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type pgconnCommandTag = interface {
	RowsAffected() int64
	String() string
}

type txKey struct{}

// TxManager guarda o limite das transacções.
//
// Uma regra: **uma operação de utilizador, uma transacção.** Gravar uma sessão
// toca em quatro tabelas — ou entra tudo, ou nada. Uma sessão sem as suas séries
// é pior do que nenhuma sessão.
type TxManager struct{ pool *pgxpool.Pool }

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// Do corre `fn` dentro de uma transacção. Aninhar é seguro: quem já está dentro
// de uma reaproveita-a, em vez de abrir outra e de se auto-bloquear.
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, already := ctx.Value(txKey{}).(pgx.Tx); already {
		return fn(ctx)
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transacção: %w", err)
	}
	// Rollback depois de Commit é um no-op em pgx: é seguro e dispensa o
	// `if err != nil { rollback }` espalhado por cada caminho de saída.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Q devolve a transacção em curso, ou o pool quando não há nenhuma.
func (m *TxManager) Q(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return txQuerier{tx}
	}
	return poolQuerier{m.pool}
}

type txQuerier struct{ tx pgx.Tx }

func (q txQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error) {
	return q.tx.Exec(ctx, sql, args...)
}
func (q txQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.tx.Query(ctx, sql, args...)
}
func (q txQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.tx.QueryRow(ctx, sql, args...)
}

type poolQuerier struct{ pool *pgxpool.Pool }

func (q poolQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error) {
	return q.pool.Exec(ctx, sql, args...)
}
func (q poolQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.pool.Query(ctx, sql, args...)
}
func (q poolQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.pool.QueryRow(ctx, sql, args...)
}
