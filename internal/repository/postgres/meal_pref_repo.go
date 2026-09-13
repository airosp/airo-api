package postgres

import (
	"context"
	"fmt"
	"time"
)

// MealPrefRepo guarda as trocas de refeição de um dia.
//
// Guarda a **variante**, não os alimentos: os alimentos derivam-se dela com o
// mesmo motor dos dois lados. Guardar a lista faria o plano deixar de
// acompanhar uma correcção no catálogo — e um alimento com as calorias erradas
// ficaria errado para sempre em quem já o tivesse no prato.
type MealPrefRepo struct{ tx *TxManager }

func NewMealPrefRepo(tx *TxManager) *MealPrefRepo { return &MealPrefRepo{tx: tx} }

// Read devolve as variantes do dia, por refeição.
func (r *MealPrefRepo) Read(ctx context.Context, userID string, day time.Time) (map[string]int, error) {
	out := map[string]int{}
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT slot, variant FROM meal_preference
		  WHERE user_id = $1 AND local_day = $2`, userID, day)
	if err != nil {
		return nil, fmt.Errorf("ler trocas de refeição: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var slot string
		var variant int
		if err := rows.Scan(&slot, &variant); err != nil {
			return nil, err
		}
		out[slot] = variant
	}
	return out, rows.Err()
}

// Bump sobe a variante de uma refeição e devolve a nova.
//
// Sobe no servidor e não no cliente porque duas trocas rápidas — o toque
// repetido de quem não gostou de nenhuma das propostas — davam a mesma variante
// se cada uma partisse do que o ecrã tinha à frente.
func (r *MealPrefRepo) Bump(ctx context.Context, userID string, day time.Time, slot string) (int, error) {
	var variant int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO meal_preference (user_id, local_day, slot, variant)
		 VALUES ($1, $2, $3, 1)
		 ON CONFLICT (user_id, local_day, slot) DO UPDATE
		    SET variant = meal_preference.variant + 1, updated_at = now()
		 RETURNING variant`, userID, day, slot).Scan(&variant)
	if err != nil {
		return 0, fmt.Errorf("trocar refeição: %w", err)
	}
	return variant, nil
}

// Reset devolve uma refeição à proposta do plano.
func (r *MealPrefRepo) Reset(ctx context.Context, userID string, day time.Time, slot string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM meal_preference WHERE user_id = $1 AND local_day = $2 AND slot = $3`,
		userID, day, slot)
	if err != nil {
		return fmt.Errorf("repor refeição: %w", err)
	}
	return nil
}
