package postgres

import (
	"context"
	"fmt"
	"time"
)

// HydrationRepo guarda o total de água por dia.
//
// Só o total: a app soma de 250 em 250 e o que se mostra é a soma. Ver o
// comentário da migração 17 para a razão de não se guardarem os copos.
type HydrationRepo struct{ tx *TxManager }

func NewHydrationRepo(tx *TxManager) *HydrationRepo { return &HydrationRepo{tx: tx} }

type HydrationDay struct {
	Day time.Time
	Ml  int
}

/*
 * Set grava o total do dia.
 *
 * **Último a escrever ganha**, e é o que se quer: o cliente manda o total, não
 * um acréscimo. Se mandasse "+250" duas vezes por causa de uma rede lenta, a
 * pessoa bebia meio litro sem sair do sofá; mandando "1750" duas vezes, o
 * resultado é 1750 nas duas.
 */
func (r *HydrationRepo) Set(ctx context.Context, userID string, day time.Time, ml int) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO hydration_day (user_id, local_day, ml)
		 VALUES ($1, $2::date, $3)
		 ON CONFLICT (user_id, local_day) DO UPDATE
		   SET ml = EXCLUDED.ml, updated_at = now()`,
		userID, day, ml)
	if err != nil {
		return fmt.Errorf("gravar água: %w", err)
	}
	return nil
}

// Days devolve os totais de um intervalo, do mais recente para o mais antigo.
func (r *HydrationRepo) Days(ctx context.Context, userID string, from, to time.Time) ([]HydrationDay, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT local_day, ml
		   FROM hydration_day
		  WHERE user_id = $1 AND local_day BETWEEN $2::date AND $3::date
		  ORDER BY local_day DESC`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("ler água: %w", err)
	}
	defer rows.Close()

	out := []HydrationDay{}
	for rows.Next() {
		var d HydrationDay
		if err := rows.Scan(&d.Day, &d.Ml); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
