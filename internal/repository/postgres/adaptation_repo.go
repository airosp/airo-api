package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdaptationRepo guarda as propostas de adaptação.
//
// ⚠️ Uma adaptação proposta **não** é uma adaptação aplicada. Nunca corre
// sozinha: é sempre a pessoa que aceita. É por isso que `applied_at` começa
// nulo e só uma acção dela o preenche.
type AdaptationRepo struct{ tx *TxManager }

func NewAdaptationRepo(tx *TxManager) *AdaptationRepo { return &AdaptationRepo{tx: tx} }

type AdaptationRow struct {
	ID        string
	JourneyID string
	Kind      string
	Payload   map[string]any
	CreatedAt time.Time
}

// Propose grava a proposta, com o assessment que a originou.
//
// Sem o assessment não há como explicar à pessoa porque é que o plano mudou — e
// um plano que muda sem explicação é um plano em que não se confia.
func (r *AdaptationRepo) Propose(ctx context.Context, journeyID, assessmentID, kind string, payload any) (string, error) {
	bruto, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("serializar adaptação: %w", err)
	}
	var id string
	err = r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO adaptation (assessment_id, journey_id, kind, payload)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		assessmentID, journeyID, kind, bruto).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gravar adaptação: %w", err)
	}
	return id, nil
}

// Pending são as que ainda esperam decisão.
func (r *AdaptationRepo) Pending(ctx context.Context, journeyID string) ([]AdaptationRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT a.id, a.journey_id, a.kind, a.payload, s.assessed_at
		   FROM adaptation a
		   JOIN assessment s ON s.id = a.assessment_id
		  WHERE a.journey_id = $1 AND a.applied_at IS NULL AND a.dismissed_at IS NULL
		  ORDER BY s.assessed_at DESC`, journeyID)
	if err != nil {
		return nil, fmt.Errorf("ler adaptações: %w", err)
	}
	defer rows.Close()

	out := []AdaptationRow{}
	for rows.Next() {
		var a AdaptationRow
		var bruto []byte
		if err := rows.Scan(&a.ID, &a.JourneyID, &a.Kind, &bruto, &a.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(bruto, &a.Payload); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// HasPending diz se já há proposta a aguardar, para não empilhar a mesma.
//
// **Uma adaptação de cada vez, pequena.** Propor três ao mesmo tempo é pedir
// que se ignorem as três.
func (r *AdaptationRepo) HasPending(ctx context.Context, journeyID string) (bool, error) {
	var existe bool
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM adaptation
		                 WHERE journey_id = $1 AND applied_at IS NULL AND dismissed_at IS NULL)`,
		journeyID).Scan(&existe)
	return existe, err
}

// Decide marca a adaptação como aplicada ou dispensada, e devolve-a.
//
// Devolve a linha para quem aplica saber o que aplicar — e a transacção é a
// mesma, para não haver o instante em que está marcada e ainda não foi feita.
func (r *AdaptationRepo) Decide(ctx context.Context, userID, id string, aplicar bool) (AdaptationRow, error) {
	coluna := "dismissed_at"
	if aplicar {
		coluna = "applied_at"
	}

	var a AdaptationRow
	var bruto []byte
	// A junção até ao utilizador é o que impede alguém de aplicar a adaptação de
	// outra pessoa com um id adivinhado.
	err := r.tx.Q(ctx).QueryRow(ctx,
		`UPDATE adaptation a
		    SET `+coluna+` = now(), applied_by = CASE WHEN $3 THEN 'user' ELSE applied_by END
		  FROM journey j JOIN goal g ON g.id = j.goal_id
		 WHERE a.id = $1 AND a.journey_id = j.id AND g.user_id = $2
		   AND a.applied_at IS NULL AND a.dismissed_at IS NULL
		 RETURNING a.id, a.journey_id, a.kind, a.payload`,
		id, userID, aplicar).Scan(&a.ID, &a.JourneyID, &a.Kind, &bruto)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdaptationRow{}, ErrNotFound
	}
	if err != nil {
		return AdaptationRow{}, fmt.Errorf("decidir adaptação: %w", err)
	}
	if err := json.Unmarshal(bruto, &a.Payload); err != nil {
		return AdaptationRow{}, err
	}
	return a, nil
}

// DismissedRecently diz se a pessoa já recusou uma proposta deste tipo há pouco.
//
// Sem isto, dispensar criava outra igual no pedido seguinte: o risco que a
// originou continua lá, e o motor volta a decidir o mesmo. Uma sugestão que
// reaparece no instante em que se recusa deixa de ser sugestão — passa a ser
// insistência.
//
// O período é o do ciclo de revisão: é quando faz sentido voltar a perguntar,
// porque é quando há factos novos para mudar a resposta.
func (r *AdaptationRepo) DismissedRecently(ctx context.Context, journeyID, kind string, desde time.Time) (bool, error) {
	var existe bool
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM adaptation
		                 WHERE journey_id = $1 AND kind = $2
		                   AND dismissed_at IS NOT NULL AND dismissed_at >= $3)`,
		journeyID, kind, desde).Scan(&existe)
	return existe, err
}
