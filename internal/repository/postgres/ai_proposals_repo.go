package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type AIProposal struct {
	ID            string
	JobID         string
	UserID        string
	TaskType      string
	Status        string
	Version       int
	SchemaVersion string
	Payload       json.RawMessage
	AppliedRef    *string
	AppliedAt     *time.Time
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

const aiProposalColumns = `id, job_id, user_id, task_type, status, version, schema_version, payload,
	applied_ref, applied_at, created_at, expires_at`

func scanProposal(row pgx.Row) (AIProposal, error) {
	var p AIProposal
	err := row.Scan(&p.ID, &p.JobID, &p.UserID, &p.TaskType, &p.Status, &p.Version, &p.SchemaVersion,
		&p.Payload, &p.AppliedRef, &p.AppliedAt, &p.CreatedAt, &p.ExpiresAt)
	return p, err
}

// CreateProposal guarda o rascunho de uma execução. Um por execução: a chave
// única em `job_id` impede uma segunda passagem de criar outro.
func (r *AIRepo) CreateProposal(ctx context.Context, p AIProposal) (AIProposal, error) {
	out, err := scanProposal(r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO ai_proposal (job_id, user_id, task_type, status, schema_version, payload, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (job_id) DO UPDATE SET updated_at = ai_proposal.updated_at
		 RETURNING `+aiProposalColumns,
		p.JobID, p.UserID, p.TaskType, p.Status, p.SchemaVersion, p.Payload, p.ExpiresAt))
	if err != nil {
		return AIProposal{}, fmt.Errorf("guardar proposta: %w", err)
	}
	return out, nil
}

// Proposal devolve uma proposta de alguém, ou nil.
func (r *AIRepo) Proposal(ctx context.Context, userID, id string) (*AIProposal, error) {
	return r.proposal(ctx, `SELECT `+aiProposalColumns+` FROM ai_proposal WHERE id = $1 AND user_id = $2`, id, userID)
}

// ProposalForJob devolve a proposta de uma execução, ou nil.
func (r *AIRepo) ProposalForJob(ctx context.Context, userID, jobID string) (*AIProposal, error) {
	return r.proposal(ctx, `SELECT `+aiProposalColumns+` FROM ai_proposal WHERE job_id = $1 AND user_id = $2`, jobID, userID)
}

// LockProposal lê a proposta e tranca-a até ao fim da transacção. É o que
// impede dois toques em "confirmar" de gravarem duas refeições.
func (r *AIRepo) LockProposal(ctx context.Context, userID, id string) (*AIProposal, error) {
	return r.proposal(ctx, `SELECT `+aiProposalColumns+` FROM ai_proposal WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID)
}

func (r *AIRepo) proposal(ctx context.Context, sql string, args ...any) (*AIProposal, error) {
	p, err := scanProposal(r.tx.Q(ctx).QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler proposta: %w", err)
	}
	return &p, nil
}

// MarkApplied fecha a proposta como aplicada, com o registo que a confirmação
// criou.
func (r *AIRepo) MarkApplied(ctx context.Context, id, ref string, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE ai_proposal SET status = 'applied', applied_ref = $2, applied_at = $3,
		        version = version + 1, updated_at = $3
		  WHERE id = $1`, id, ref, now)
	if err != nil {
		return fmt.Errorf("aplicar proposta: %w", err)
	}
	return nil
}

// MarkDismissed fecha a proposta como dispensada.
func (r *AIRepo) MarkDismissed(ctx context.Context, id string, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE ai_proposal SET status = 'dismissed', version = version + 1, updated_at = $2
		  WHERE id = $1`, id, now)
	if err != nil {
		return fmt.Errorf("dispensar proposta: %w", err)
	}
	return nil
}
