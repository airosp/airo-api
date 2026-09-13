package postgres

import (
	"context"
	"fmt"
	"time"
)

// ProgressRepo lê o que o progresso precisa: a série de medições e as sessões.
//
// Separado dos outros repositórios porque a pergunta é outra: aqui não se lê o
// estado de nada, lê-se **história** — e história lê-se por intervalo.
type ProgressRepo struct{ tx *TxManager }

func NewProgressRepo(tx *TxManager) *ProgressRepo { return &ProgressRepo{tx: tx} }

type MeasurementRow struct {
	Metric     string
	Value      float64
	Unit       string
	RecordedAt time.Time
}

// Measurements devolve a série de uma métrica, da mais antiga para a mais
// recente.
//
// O peso **é uma série**, não um campo. Tratá-lo como campo foi o que tornou
// impossível calcular tendência.
func (r *ProgressRepo) Measurements(ctx context.Context, userID, metric string, since time.Time) ([]MeasurementRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT metric, value, unit, recorded_at
		   FROM measurement
		  WHERE user_id = $1 AND metric = $2 AND recorded_at >= $3
		  ORDER BY recorded_at`, userID, metric, since)
	if err != nil {
		return nil, fmt.Errorf("ler medições: %w", err)
	}
	defer rows.Close()

	out := []MeasurementRow{}
	for rows.Next() {
		var m MeasurementRow
		if err := rows.Scan(&m.Metric, &m.Value, &m.Unit, &m.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SessionsSince devolve as sessões de um período, para a adesão e os riscos.
func (r *ProgressRepo) SessionsSince(ctx context.Context, userID string, since time.Time) ([]HistoryRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, status, occurred_at, local_day, planned_seconds, duration_seconds,
		        sets_planned, sets_done
		   FROM workout_session
		  WHERE user_id = $1 AND local_day >= $2
		  ORDER BY occurred_at`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("ler sessões: %w", err)
	}
	defer rows.Close()

	out := []HistoryRow{}
	for rows.Next() {
		var h HistoryRow
		if err := rows.Scan(&h.ID, &h.Status, &h.OccurredAt, &h.LocalDay,
			&h.PlannedSeconds, &h.DurationSeconds, &h.SetsPlanned, &h.SetsDone); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
