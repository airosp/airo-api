package postgres

import (
	"context"
	"fmt"
	"time"
)

// CalendarRepo guarda as excepções, ausências e notas de datas concretas.
type CalendarRepo struct{ tx *TxManager }

func NewCalendarRepo(tx *TxManager) *CalendarRepo { return &CalendarRepo{tx: tx} }

type MarkRow struct {
	ID    string
	Kind  string
	Day   time.Time
	Until time.Time
	Text  string
}

// Save grava ou substitui uma marca.
//
// `ON CONFLICT` pelo identificador do telemóvel: a marca nasce offline e é ela
// que lhe dá o nome. Reenviar é mandar a mesma marca, não uma segunda — que
// numa rede fraca acontece sempre.
func (r *CalendarRepo) Save(ctx context.Context, userID string, m MarkRow) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO calendar_mark (id, user_id, kind, day, until, text)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6,''))
		 ON CONFLICT (user_id, id) DO UPDATE
		    SET kind = EXCLUDED.kind, day = EXCLUDED.day,
		        until = EXCLUDED.until, text = EXCLUDED.text`,
		m.ID, userID, m.Kind, m.Day, m.Until, m.Text)
	if err != nil {
		return fmt.Errorf("gravar marca: %w", err)
	}
	return nil
}

// Marks devolve as marcas de um intervalo, inclusive nas duas pontas.
func (r *CalendarRepo) Marks(ctx context.Context, userID string, from, to time.Time) ([]MarkRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, kind, day, until, COALESCE(text,'')
		   FROM calendar_mark
		  WHERE user_id = $1 AND day <= $3 AND until >= $2
		  ORDER BY day, id`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("ler marcas: %w", err)
	}
	defer rows.Close()

	out := []MarkRow{}
	for rows.Next() {
		var m MarkRow
		if err := rows.Scan(&m.ID, &m.Kind, &m.Day, &m.Until, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Delete apaga uma marca.
func (r *CalendarRepo) Delete(ctx context.Context, userID, id string) (bool, error) {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM calendar_mark WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return false, fmt.Errorf("apagar marca: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// AbsenceDays conta os dias de ausência dentro de um intervalo.
//
// É o que sai do denominador da adesão. Conta dias **distintos**: duas
// ausências que se sobrepõem não tiram o dobro dos dias, e sem o `DISTINCT` a
// adesão de quem marcasse duas vezes a mesma semana passava dos 100%.
func (r *CalendarRepo) AbsenceDays(ctx context.Context, userID string, from, to time.Time) (int, error) {
	var dias int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT count(DISTINCT d)
		   FROM calendar_mark m,
		        LATERAL generate_series(GREATEST(m.day, $2::date),
		                                LEAST(m.until, $3::date),
		                                interval '1 day') AS d
		  WHERE m.user_id = $1 AND m.kind = 'absence'
		    AND m.day <= $3 AND m.until >= $2`, userID, from, to).Scan(&dias)
	if err != nil {
		return 0, fmt.Errorf("contar ausências: %w", err)
	}
	return dias, nil
}
