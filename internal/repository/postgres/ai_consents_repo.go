package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AIRepo guarda o caminho da camada de inteligência: autorizações, execuções,
// propostas, consumo e auditoria. Dividido por ficheiros, um por tabela.
type AIRepo struct{ tx *TxManager }

func NewAIRepo(tx *TxManager) *AIRepo { return &AIRepo{tx: tx} }

// Tx corre `fn` numa transacção — para o serviço juntar escritas que têm de
// entrar todas ou nenhuma.
func (r *AIRepo) Tx(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.tx.Do(ctx, fn)
}

type AIConsent struct {
	ID            string
	Purpose       string
	PolicyVersion string
	GrantedAt     time.Time
}

// ActiveConsents devolve as autorizações em vigor, por finalidade.
func (r *AIRepo) ActiveConsents(ctx context.Context, userID string) (map[string]AIConsent, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, purpose, policy_version, granted_at
		   FROM ai_consent WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return nil, fmt.Errorf("ler consentimentos: %w", err)
	}
	defer rows.Close()
	out := map[string]AIConsent{}
	for rows.Next() {
		var c AIConsent
		if err := rows.Scan(&c.ID, &c.Purpose, &c.PolicyVersion, &c.GrantedAt); err != nil {
			return nil, fmt.Errorf("ler consentimento: %w", err)
		}
		out[c.Purpose] = c
	}
	return out, rows.Err()
}

// ActiveConsent devolve a autorização em vigor para uma finalidade, ou nil.
func (r *AIRepo) ActiveConsent(ctx context.Context, userID, purpose string) (*AIConsent, error) {
	var c AIConsent
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id, purpose, policy_version, granted_at
		   FROM ai_consent WHERE user_id = $1 AND purpose = $2 AND revoked_at IS NULL`,
		userID, purpose).Scan(&c.ID, &c.Purpose, &c.PolicyVersion, &c.GrantedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler consentimento: %w", err)
	}
	return &c, nil
}

/*
 * GrantConsent regista uma autorização.
 *
 * Se já houver uma em vigor para a mesma versão, devolve-a: autorizar duas
 * vezes é estar autorizado. Se for de uma versão antiga, fecha-a e abre outra
 * — a pessoa leu o texto novo, e é esse que passa a valer.
 */
func (r *AIRepo) GrantConsent(ctx context.Context, userID, purpose, version string, now time.Time) (AIConsent, error) {
	var out AIConsent
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		cur, err := r.ActiveConsent(ctx, userID, purpose)
		if err != nil {
			return err
		}
		if cur != nil && cur.PolicyVersion == version {
			out = *cur
			return nil
		}
		if cur != nil {
			if _, err := r.tx.Q(ctx).Exec(ctx,
				`UPDATE ai_consent SET revoked_at = $2 WHERE id = $1`, cur.ID, now); err != nil {
				return fmt.Errorf("fechar consentimento antigo: %w", err)
			}
		}
		out = AIConsent{Purpose: purpose, PolicyVersion: version, GrantedAt: now}
		return r.tx.Q(ctx).QueryRow(ctx,
			`INSERT INTO ai_consent (user_id, purpose, policy_version, granted_at)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			userID, purpose, version, now).Scan(&out.ID)
	})
	return out, err
}

/*
 * RevokeConsent fecha a autorização e trava o que ainda estava por fazer.
 *
 * Revogar tem de impedir **novas** análises — incluindo as que estavam na fila
 * à espera de vez. Essas são canceladas, e o texto que guardavam é apagado. O
 * que já foi analisado fica como rascunho até expirar, porque já não sai da
 * Airo.
 */
func (r *AIRepo) RevokeConsent(ctx context.Context, userID, purpose, task string, now time.Time) (bool, error) {
	var revoked bool
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		tag, err := r.tx.Q(ctx).Exec(ctx,
			`UPDATE ai_consent SET revoked_at = $3
			  WHERE user_id = $1 AND purpose = $2 AND revoked_at IS NULL`, userID, purpose, now)
		if err != nil {
			return fmt.Errorf("revogar consentimento: %w", err)
		}
		revoked = tag.RowsAffected() > 0
		if _, err := r.tx.Q(ctx).Exec(ctx,
			`UPDATE ai_job SET status = 'cancelled', error_code = 'consent_revoked',
			        input_ref = '{}'::jsonb, lease_until = NULL, updated_at = $3
			  WHERE user_id = $1 AND task_type = $2 AND status IN ('queued','processing','validating')`,
			userID, task, now); err != nil {
			return fmt.Errorf("cancelar execuções pendentes: %w", err)
		}
		return nil
	})
	return revoked, err
}
