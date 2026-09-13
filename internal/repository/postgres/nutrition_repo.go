package postgres

import (
	"context"
	"fmt"
	"time"
)

// MealLog é um registo do diário alimentar.
//
// O peso está no `ClientID`: é o telemóvel que o dá, porque o registo nasce
// offline — a pessoa come, regista, e a rede aparece depois.
type MealLog struct {
	ClientID     string
	Slot         string
	Status       string
	Portion      float64
	Kcal         int
	Protein      float64
	Carbs        float64
	Fat          float64
	Source       string
	Label        *string
	PortionLabel *string
	PhotoURL     *string
	PhotoThumb   *string
	RecordedAt   time.Time
	LocalDay     time.Time
}

type NutritionRepo struct{ tx *TxManager }

func NewNutritionRepo(tx *TxManager) *NutritionRepo { return &NutritionRepo{tx: tx} }

// SaveLog grava ou actualiza um registo.
//
// `ON CONFLICT` sobre (user_id, client_id): reenviar o mesmo registo é o caso
// normal, não um erro. Uma rede fraca faz o telemóvel tentar outra vez, e sem
// isto o almoço aparecia três vezes.
func (r *NutritionRepo) SaveLog(ctx context.Context, userID string, l MealLog) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO nutrition_log (
		     user_id, client_id, slot, status, portion, kcal,
		     protein_g, carbs_g, fat_g, source, label, portion_label,
		     photo_url, photo_thumb_url, recorded_at, local_day)
		 VALUES ($1,$2,$3::meal_slot,$4::log_status,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 ON CONFLICT (user_id, client_id) WHERE client_id IS NOT NULL DO UPDATE SET
		     slot = EXCLUDED.slot,
		     status = EXCLUDED.status,
		     portion = EXCLUDED.portion,
		     kcal = EXCLUDED.kcal,
		     protein_g = EXCLUDED.protein_g,
		     carbs_g = EXCLUDED.carbs_g,
		     fat_g = EXCLUDED.fat_g,
		     source = EXCLUDED.source,
		     label = EXCLUDED.label,
		     portion_label = EXCLUDED.portion_label,
		     photo_url = EXCLUDED.photo_url,
		     photo_thumb_url = EXCLUDED.photo_thumb_url,
		     recorded_at = EXCLUDED.recorded_at,
		     local_day = EXCLUDED.local_day`,
		userID, l.ClientID, l.Slot, l.Status, l.Portion, l.Kcal,
		l.Protein, l.Carbs, l.Fat, l.Source, l.Label, l.PortionLabel,
		l.PhotoURL, l.PhotoThumb, l.RecordedAt, l.LocalDay)
	if err != nil {
		return fmt.Errorf("gravar registo alimentar: %w", err)
	}
	return nil
}

// Logs devolve os registos de um intervalo de dias, inclusive.
func (r *NutritionRepo) Logs(ctx context.Context, userID string, from, to time.Time) ([]MealLog, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT client_id, slot::text, status::text, portion, kcal,
		        protein_g, carbs_g, fat_g, source, label, portion_label,
		        photo_url, photo_thumb_url, recorded_at, local_day
		   FROM nutrition_log
		  WHERE user_id = $1 AND local_day BETWEEN $2 AND $3
		  ORDER BY recorded_at`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("ler registos: %w", err)
	}
	defer rows.Close()

	var out []MealLog
	for rows.Next() {
		var l MealLog
		var clientID *string
		if err := rows.Scan(&clientID, &l.Slot, &l.Status, &l.Portion, &l.Kcal,
			&l.Protein, &l.Carbs, &l.Fat, &l.Source, &l.Label, &l.PortionLabel,
			&l.PhotoURL, &l.PhotoThumb, &l.RecordedAt, &l.LocalDay); err != nil {
			return nil, fmt.Errorf("ler registo: %w", err)
		}
		if clientID != nil {
			l.ClientID = *clientID
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteLog apaga um registo. Devolve se existia.
//
// Apagar o que já não está não é erro — é o resultado que se queria. Mas dizer
// se existia permite ao cliente saber se o que apagou era o que pensava.
func (r *NutritionRepo) DeleteLog(ctx context.Context, userID, clientID string) (bool, error) {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM nutrition_log WHERE user_id = $1 AND client_id = $2`, userID, clientID)
	if err != nil {
		return false, fmt.Errorf("apagar registo: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
