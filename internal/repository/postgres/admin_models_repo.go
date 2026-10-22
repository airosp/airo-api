package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AdminModelsRepo guarda os modelos validados por profissionais: treinos e
// dietas. O conteúdo é jsonb porque é a forma do editor; as contas que dele
// saem fazem-se no handler, com o motor, e não se guardam.
type AdminModelsRepo struct{ tx *TxManager }

func NewAdminModelsRepo(tx *TxManager) *AdminModelsRepo { return &AdminModelsRepo{tx: tx} }

type TemplateRow struct {
	ID        string
	Name      string
	Focus     string
	Level     string
	Minutes   int
	Goal      string
	Status    string
	Blocks    json.RawMessage
	UpdatedAt time.Time
	UpdatedBy string
}

func (r *AdminModelsRepo) Templates(ctx context.Context, id string) ([]TemplateRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, name, focus::text, level::text, minutes, goal, status, blocks, updated_at, updated_by
		   FROM workout_template WHERE ($1 = '' OR id = $1) ORDER BY updated_at DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("ler modelos de treino: %w", err)
	}
	defer rows.Close()
	out := []TemplateRow{}
	for rows.Next() {
		var t TemplateRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Focus, &t.Level, &t.Minutes, &t.Goal, &t.Status,
			&t.Blocks, &t.UpdatedAt, &t.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *AdminModelsRepo) SaveTemplate(ctx context.Context, t TemplateRow, create bool) error {
	args := []any{t.ID, t.Name, t.Focus, t.Level, t.Minutes, t.Goal, t.Status, t.Blocks, t.UpdatedBy}
	if create {
		_, err := r.tx.Q(ctx).Exec(ctx,
			`INSERT INTO workout_template (id, name, focus, level, minutes, goal, status, blocks, updated_by)
			 VALUES ($1,$2,$3::session_focus,$4::experience,$5,$6,$7,$8,$9)`, args...)
		if uniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_template SET name=$2, focus=$3::session_focus, level=$4::experience, minutes=$5,
		   goal=$6, status=$7, blocks=$8, updated_by=$9, updated_at=now() WHERE id=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *AdminModelsRepo) DeleteTemplate(ctx context.Context, id string) error {
	tag, err := r.tx.Q(ctx).Exec(ctx, `DELETE FROM workout_template WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type DietModelRow struct {
	ID          string
	Name        string
	Goal        string
	MealsPerDay int
	Status      string
	Rules       json.RawMessage
	Week        json.RawMessage
	Comments    json.RawMessage
	UpdatedAt   time.Time
	UpdatedBy   string
	ApprovedBy  *string
}

func (r *AdminModelsRepo) DietModels(ctx context.Context, id string) ([]DietModelRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, name, goal::text, meals_per_day, status, rules, week, comments, updated_at, updated_by, approved_by
		   FROM diet_model WHERE ($1 = '' OR id = $1) ORDER BY updated_at DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("ler modelos de dieta: %w", err)
	}
	defer rows.Close()
	out := []DietModelRow{}
	for rows.Next() {
		var d DietModelRow
		if err := rows.Scan(&d.ID, &d.Name, &d.Goal, &d.MealsPerDay, &d.Status, &d.Rules, &d.Week,
			&d.Comments, &d.UpdatedAt, &d.UpdatedBy, &d.ApprovedBy); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveDietModel grava o modelo. Editar um modelo aprovado devolve-o a rascunho:
// o que a nutricionista aprovou foi outra coisa.
func (r *AdminModelsRepo) SaveDietModel(ctx context.Context, d DietModelRow, create bool) error {
	args := []any{d.ID, d.Name, d.Goal, d.MealsPerDay, d.Rules, d.Week, d.UpdatedBy}
	if create {
		_, err := r.tx.Q(ctx).Exec(ctx,
			`INSERT INTO diet_model (id, name, goal, meals_per_day, rules, week, updated_by)
			 VALUES ($1,$2,$3::nutrition_goal,$4,$5,$6,$7)`, args...)
		if uniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE diet_model SET name=$2, goal=$3::nutrition_goal, meals_per_day=$4, rules=$5, week=$6,
		   updated_by=$7, updated_at=now(),
		   status = CASE WHEN status = 'approved' THEN 'draft' ELSE status END,
		   approved_by = CASE WHEN status = 'approved' THEN NULL ELSE approved_by END
		 WHERE id=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *AdminModelsRepo) SetDietStatus(ctx context.Context, id, status, by string, comment json.RawMessage) error {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE diet_model SET status=$2, updated_at=now(), updated_by=$3,
		   approved_by = CASE WHEN $2 = 'approved' THEN $3 ELSE NULL END,
		   comments = CASE WHEN $4::jsonb IS NULL THEN comments ELSE comments || $4::jsonb END
		 WHERE id=$1`, id, status, by, comment)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
