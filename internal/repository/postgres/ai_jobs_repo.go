package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type AIJob struct {
	ID             string
	UserID         string
	TaskType       string
	Status         string
	SchemaVersion  string
	ConsentID      *string
	ModelVersion   *string
	IdempotencyKey string
	InputRef       json.RawMessage
	ResultRef      json.RawMessage
	ErrorCode      *string
	Attempts       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
}

const aiJobColumns = `id, user_id, task_type, status, schema_version, consent_id, model_version,
	idempotency_key, input_ref, result_ref, error_code, attempts, created_at, updated_at, expires_at`

func scanJob(row pgx.Row) (AIJob, error) {
	var j AIJob
	err := row.Scan(&j.ID, &j.UserID, &j.TaskType, &j.Status, &j.SchemaVersion, &j.ConsentID,
		&j.ModelVersion, &j.IdempotencyKey, &j.InputRef, &j.ResultRef, &j.ErrorCode, &j.Attempts,
		&j.CreatedAt, &j.UpdatedAt, &j.ExpiresAt)
	return j, err
}

// NewAIJob é o que se precisa para pôr uma execução na fila.
type NewAIJob struct {
	UserID         string
	TaskType       string
	SchemaVersion  string
	ConsentID      string
	IdempotencyKey string
	InputRef       json.RawMessage
	ExpiresAt      time.Time
}

/*
 * CreateJob põe uma execução na fila, uma só vez por chave.
 *
 * A chave de idempotência é do telemóvel. Reenviar o mesmo pedido — numa rede
 * fraca acontece sempre — devolve a execução que já existe em vez de pagar
 * outra análise da mesma fotografia. `created` diz qual dos dois aconteceu.
 */
func (r *AIRepo) CreateJob(ctx context.Context, in NewAIJob) (AIJob, bool, error) {
	j, err := scanJob(r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO ai_job (user_id, task_type, schema_version, consent_id, idempotency_key, input_ref, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (user_id, task_type, idempotency_key) DO NOTHING
		 RETURNING `+aiJobColumns,
		in.UserID, in.TaskType, in.SchemaVersion, in.ConsentID, in.IdempotencyKey, in.InputRef, in.ExpiresAt))
	if err == nil {
		return j, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AIJob{}, false, fmt.Errorf("criar execução: %w", err)
	}
	j, err = scanJob(r.tx.Q(ctx).QueryRow(ctx,
		`SELECT `+aiJobColumns+` FROM ai_job
		  WHERE user_id = $1 AND task_type = $2 AND idempotency_key = $3`,
		in.UserID, in.TaskType, in.IdempotencyKey))
	if err != nil {
		return AIJob{}, false, fmt.Errorf("ler execução existente: %w", err)
	}
	return j, false, nil
}

// Job devolve uma execução de alguém. De outra pessoa é como se não existisse.
func (r *AIRepo) Job(ctx context.Context, userID, id string) (*AIJob, error) {
	j, err := scanJob(r.tx.Q(ctx).QueryRow(ctx,
		`SELECT `+aiJobColumns+` FROM ai_job WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler execução: %w", err)
	}
	return &j, nil
}

// CountJobsSince conta os pedidos de alguém, para o limite diário. Contam os
// que custaram ou podem vir a custar — os cancelados antes de correr não.
func (r *AIRepo) CountJobsSince(ctx context.Context, userID, task string, since time.Time) (int, error) {
	var n int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM ai_job
		  WHERE user_id = $1 AND task_type = $2 AND created_at >= $3 AND status <> 'cancelled'`,
		userID, task, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("contar execuções: %w", err)
	}
	return n, nil
}

/*
 * ClaimJob reserva a próxima execução para este processo.
 *
 * `FOR UPDATE SKIP LOCKED`: com várias réplicas, cada uma apanha uma linha
 * diferente sem esperar pelas outras. O prazo (`lease_until`) é o que salva
 * uma execução cujo processo morreu a meio — passado o prazo, volta a estar
 * disponível. E o tecto de tentativas impede uma fotografia envenenada de
 * rodar para sempre.
 */
func (r *AIRepo) ClaimJob(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int) (*AIJob, error) {
	j, err := scanJob(r.tx.Q(ctx).QueryRow(ctx,
		`UPDATE ai_job SET status = 'processing', attempts = attempts + 1,
		        lease_until = $1::timestamptz + $2::interval, updated_at = $1
		  WHERE id = (
		      SELECT id FROM ai_job
		       WHERE ((status = 'queued' AND run_after <= $1)
		              OR (status IN ('processing','validating') AND lease_until < $1))
		         AND attempts < $3 AND expires_at > $1
		       ORDER BY run_after
		       FOR UPDATE SKIP LOCKED
		       LIMIT 1)
		 RETURNING `+aiJobColumns,
		now, fmt.Sprintf("%d milliseconds", lease.Milliseconds()), maxAttempts))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reservar execução: %w", err)
	}
	return &j, nil
}

// MarkValidating regista que a resposta chegou e está a ser validada.
func (r *AIRepo) MarkValidating(ctx context.Context, id, model string, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE ai_job SET status = 'validating', model_version = $2, updated_at = $3
		  WHERE id = $1 AND status = 'processing'`, id, model, now)
	if err != nil {
		return fmt.Errorf("marcar a validar: %w", err)
	}
	return nil
}

/*
 * FinishJob fecha uma execução.
 *
 * O texto que a pessoa escreveu sai aqui de `input_ref`: serviu para a
 * análise e não serve para mais nada. Fica a referência à proposta, que é o
 * que se precisa para investigar.
 */
func (r *AIRepo) FinishJob(ctx context.Context, id, status string, errorCode *string, resultRef json.RawMessage, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE ai_job SET status = $2, error_code = $3, result_ref = $4,
		        input_ref = input_ref - 'text', lease_until = NULL, updated_at = $5
		  WHERE id = $1`, id, status, errorCode, resultRef, now)
	if err != nil {
		return fmt.Errorf("fechar execução: %w", err)
	}
	return nil
}

// RetryLater devolve a execução à fila para uma nova tentativa, depois de `at`.
func (r *AIRepo) RetryLater(ctx context.Context, id, errorCode string, at, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE ai_job SET status = 'queued', error_code = $2, run_after = $3,
		        lease_until = NULL, updated_at = $4
		  WHERE id = $1 AND status IN ('processing','validating')`, id, errorCode, at, now)
	if err != nil {
		return fmt.Errorf("devolver à fila: %w", err)
	}
	return nil
}

// SetJobStatus muda o estado de uma execução já fechada — quando a proposta é
// aplicada ou dispensada.
func (r *AIRepo) SetJobStatus(ctx context.Context, id, status string, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE ai_job SET status = $2, updated_at = $3 WHERE id = $1`, id, status, now)
	if err != nil {
		return fmt.Errorf("mudar estado da execução: %w", err)
	}
	return nil
}

// RetentionResult é o que uma passagem de limpeza fez.
type RetentionResult struct {
	ExpiredJobs, ExpiredProposals, Deleted, Audit int64
}

/*
 * ApplyRetention expira o que ficou por fazer e apaga o que já não serve.
 *
 * Uma execução que nunca correu até ao prazo expira, em vez de ficar "a
 * analisar" para sempre. Um rascunho que ninguém confirmou expira também. E o
 * que é antigo sai de vez — o registo no diário fica, que é o que é da
 * pessoa; o rasto de como lá chegou não precisa de durar mais do que isso.
 */
func (r *AIRepo) ApplyRetention(ctx context.Context, now time.Time, keepFor, auditFor time.Duration) (RetentionResult, error) {
	var out RetentionResult
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		tag, err := r.tx.Q(ctx).Exec(ctx,
			`UPDATE ai_job SET status = 'expired', input_ref = '{}'::jsonb, lease_until = NULL, updated_at = $1
			  WHERE status IN ('queued','processing','validating') AND expires_at <= $1`, now)
		if err != nil {
			return fmt.Errorf("expirar execuções: %w", err)
		}
		out.ExpiredJobs = tag.RowsAffected()

		tag, err = r.tx.Q(ctx).Exec(ctx,
			`WITH exp AS (
			     UPDATE ai_proposal SET status = 'expired', updated_at = $1
			      WHERE status IN ('ready_for_review','needs_correction') AND expires_at <= $1
			  RETURNING job_id)
			 UPDATE ai_job SET status = 'expired', updated_at = $1 WHERE id IN (SELECT job_id FROM exp)`, now)
		if err != nil {
			return fmt.Errorf("expirar propostas: %w", err)
		}
		out.ExpiredProposals = tag.RowsAffected()

		tag, err = r.tx.Q(ctx).Exec(ctx,
			`DELETE FROM ai_job WHERE created_at < $1::timestamptz - $2::interval`,
			now, fmt.Sprintf("%d seconds", int64(keepFor.Seconds())))
		if err != nil {
			return fmt.Errorf("apagar execuções antigas: %w", err)
		}
		out.Deleted = tag.RowsAffected()

		tag, err = r.tx.Q(ctx).Exec(ctx,
			`DELETE FROM ai_audit_event WHERE created_at < $1::timestamptz - $2::interval`,
			now, fmt.Sprintf("%d seconds", int64(auditFor.Seconds())))
		if err != nil {
			return fmt.Errorf("apagar auditoria antiga: %w", err)
		}
		out.Audit = tag.RowsAffected()
		return nil
	})
	return out, err
}
