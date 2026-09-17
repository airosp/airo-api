package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

/*
 * ShoppingRepo guarda o que a pessoa decidiu sobre a lista de compras.
 *
 * Não guarda a lista: essa sai do plano alimentar, e uma segunda cópia
 * envelhecia sozinha. Ver o comentário da migração 21.
 */
type ShoppingRepo struct{ tx *TxManager }

func NewShoppingRepo(tx *TxManager) *ShoppingRepo { return &ShoppingRepo{tx: tx} }

// Set grava o estado de uma semana. Último a escrever ganha, como a água.
func (r *ShoppingRepo) Set(ctx context.Context, userID string, semana time.Time, estado json.RawMessage) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO shopping_list (user_id, week_start, state)
		 VALUES ($1, $2::date, $3)
		 ON CONFLICT (user_id, week_start) DO UPDATE
		   SET state = EXCLUDED.state, updated_at = now()`,
		userID, semana, []byte(estado))
	if err != nil {
		return fmt.Errorf("gravar lista de compras: %w", err)
	}
	return nil
}

/*
 * Get devolve o estado de uma semana.
 *
 * `ErrNotFound` quando a semana ainda não foi tocada — quem nunca riscou nada
 * não tem estado, e inventar-lhe um vazio era dizer que já tinha vindo cá.
 */
func (r *ShoppingRepo) Get(ctx context.Context, userID string, semana time.Time) (json.RawMessage, error) {
	var bruto []byte
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT state FROM shopping_list
		  WHERE user_id = $1 AND week_start = $2::date`, userID, semana).Scan(&bruto)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ler lista de compras: %w", err)
	}
	return json.RawMessage(bruto), nil
}

/*
 * Prune apaga as semanas passadas.
 *
 * A lista da semana passada não serve para nada e é comida a ocupar espaço.
 * Corre no mesmo pedido que grava: não vale um trabalho agendado só para isto.
 */
func (r *ShoppingRepo) Prune(ctx context.Context, userID string, antesDe time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM shopping_list WHERE user_id = $1 AND week_start < $2::date`, userID, antesDe)
	if err != nil {
		return fmt.Errorf("limpar listas antigas: %w", err)
	}
	return nil
}
