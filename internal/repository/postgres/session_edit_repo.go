package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

/*
 * SessionEditRepo guarda as edições ao treino de um dia.
 *
 * Um documento por dia. Ver o comentário da migração 20 para a razão de não se
 * guardar uma linha por alteração.
 */
type SessionEditRepo struct{ tx *TxManager }

func NewSessionEditRepo(tx *TxManager) *SessionEditRepo { return &SessionEditRepo{tx: tx} }

// EdicaoDeDia é o que ficou guardado para um dia.
type EdicaoDeDia struct {
	Day   time.Time
	Edits json.RawMessage
}

/*
 * Set grava as edições de um dia.
 *
 * **Último a escrever ganha.** O cliente manda o estado do ecrã, não um
 * acréscimo — mandar duas vezes o mesmo dia dá o mesmo dia, e é o que salva
 * uma rede que repete pedidos.
 */
func (r *SessionEditRepo) Set(ctx context.Context, userID string, day time.Time, edits json.RawMessage) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO session_edit (user_id, local_day, edits)
		 VALUES ($1, $2::date, $3)
		 ON CONFLICT (user_id, local_day) DO UPDATE
		   SET edits = EXCLUDED.edits, updated_at = now()`,
		userID, day, []byte(edits))
	if err != nil {
		return fmt.Errorf("gravar edições do dia: %w", err)
	}
	return nil
}

/*
 * Clear apaga as edições de um dia — voltar ao treino que o motor propõe.
 *
 * Apagar a linha em vez de guardar um documento vazio: "não mexi em nada" e
 * "desfiz o que tinha mexido" acabam no mesmo sítio, e é assim que deve ser.
 */
func (r *SessionEditRepo) Clear(ctx context.Context, userID string, day time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM session_edit WHERE user_id = $1 AND local_day = $2::date`, userID, day)
	if err != nil {
		return fmt.Errorf("apagar edições do dia: %w", err)
	}
	return nil
}

/*
 * Since devolve as edições a partir de um dia, da mais recente para a mais
 * antiga.
 *
 * O cliente pede poucos dias — o de hoje interessa-lhe sempre, e os de trás
 * servem para o histórico não mentir sobre o que estava proposto.
 */
func (r *SessionEditRepo) Since(ctx context.Context, userID string, from time.Time) ([]EdicaoDeDia, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT local_day, edits
		   FROM session_edit
		  WHERE user_id = $1 AND local_day >= $2::date
		  ORDER BY local_day DESC`, userID, from)
	if err != nil {
		return nil, fmt.Errorf("ler edições do dia: %w", err)
	}
	defer rows.Close()

	out := []EdicaoDeDia{}
	for rows.Next() {
		var e EdicaoDeDia
		var bruto []byte
		if err := rows.Scan(&e.Day, &bruto); err != nil {
			return nil, err
		}
		e.Edits = json.RawMessage(bruto)
		out = append(out, e)
	}
	return out, rows.Err()
}
