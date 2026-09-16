package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// MeasurementEntry é um ponto da série, com o que o ecrã precisa de mostrar.
//
// Ao contrário de `MeasurementRow`, traz o identificador e a nota: a série do
// progresso só precisa de números, mas a lista de pesos precisa de dizer qual
// é qual e o que a pessoa escreveu ao lado.
type MeasurementEntry struct {
	ID         string
	Metric     string
	Value      float64
	Unit       string
	RecordedAt time.Time
	Note       string
}

// Series devolve a série completa de uma métrica, da mais recente para a mais
// antiga — que é a ordem por que se lê uma lista de pesos.
func (r *ProgressRepo) Series(ctx context.Context, userID, metric string, since time.Time) ([]MeasurementEntry, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id::text, metric::text, value, unit, recorded_at, COALESCE(note,'')
		   FROM measurement
		  WHERE user_id = $1 AND metric = $2::metric_key AND recorded_at >= $3
		  ORDER BY recorded_at DESC`, userID, metric, since)
	if err != nil {
		return nil, fmt.Errorf("ler série: %w", err)
	}
	defer rows.Close()

	out := []MeasurementEntry{}
	for rows.Next() {
		var m MeasurementEntry
		if err := rows.Scan(&m.ID, &m.Metric, &m.Value, &m.Unit, &m.RecordedAt, &m.Note); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

/*
 * AddMeasurement acrescenta um ponto à série.
 *
 * ⚠️ **Não repete um ponto que já lá está.** O peso do dia chega por dois
 * caminhos — o ecrã de evolução e a gravação do perfil, que também o regista —
 * e sem esta guarda o mesmo número aparecia duas vezes na lista de quem só o
 * escreveu uma. Mesmo dia, mesma métrica e mesmo valor é a mesma medição.
 */
func (r *ProgressRepo) AddMeasurement(ctx context.Context, userID string, m MeasurementEntry) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO measurement (user_id, metric, value, unit, recorded_at, source, note)
		 SELECT $1, $2::metric_key, $3, $4, $5::timestamptz, 'manual', NULLIF($6,'')
		  WHERE NOT EXISTS (
		    SELECT 1 FROM measurement
		     WHERE user_id = $1 AND metric = $2::metric_key AND value = $3
		       -- O mesmo parâmetro serve de carimbo e de dia; sem o molde
		       -- explícito o Postgres não deduz um tipo só para os dois.
		       AND recorded_at::date = ($5::timestamptz)::date
		  )
		 RETURNING id::text`,
		userID, m.Metric, m.Value, m.Unit, m.RecordedAt, m.Note).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Já lá estava. Não é erro: é o mesmo ponto.
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("gravar medição: %w", err)
	}
	return id, nil
}
