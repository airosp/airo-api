package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AIUsage é uma chamada ao fornecedor e o que custou.
type AIUsage struct {
	UserID       string
	JobID        string
	TaskType     string
	Model        string
	RequestID    string
	InputTokens  int64
	OutputTokens int64
	CostMicroUSD int64
	LatencyMs    int
	Outcome      string
}

// RecordUsage regista uma chamada. Regista-se sempre, falhada ou não: uma
// resposta cortada também se pagou.
func (r *AIRepo) RecordUsage(ctx context.Context, u AIUsage) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO ai_usage (user_id, job_id, task_type, model, request_id, input_tokens,
		                       output_tokens, cost_micro_usd, latency_ms, outcome)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10)`,
		u.UserID, u.JobID, u.TaskType, u.Model, u.RequestID, u.InputTokens, u.OutputTokens,
		u.CostMicroUSD, u.LatencyMs, u.Outcome)
	if err != nil {
		return fmt.Errorf("registar consumo: %w", err)
	}
	return nil
}

// SpentSince é o gasto da instalação inteira desde `since`, em milionésimos
// de dólar.
func (r *AIRepo) SpentSince(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	if err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT coalesce(sum(cost_micro_usd), 0) FROM ai_usage WHERE created_at >= $1`, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("somar consumo: %w", err)
	}
	return n, nil
}

// AIAudit é um acontecimento do trilho. `Detail` leva referências e metadados,
// nunca conteúdo.
type AIAudit struct {
	UserID     string
	JobID      string
	ProposalID string
	Kind       string
	Detail     map[string]any
}

func (r *AIRepo) Audit(ctx context.Context, a AIAudit) error {
	detail, err := json.Marshal(a.Detail)
	if err != nil || a.Detail == nil {
		detail = []byte("{}")
	}
	_, err = r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO ai_audit_event (user_id, job_id, proposal_id, kind, detail)
		 VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5)`,
		a.UserID, a.JobID, a.ProposalID, a.Kind, detail)
	if err != nil {
		return fmt.Errorf("auditar: %w", err)
	}
	return nil
}
