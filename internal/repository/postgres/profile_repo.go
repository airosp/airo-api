package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProfileRow é o perfil como está gravado.
//
// Tipos próprios e não os do serviço: o repositório não pode importar o serviço,
// que já o importa a ele. A tradução faz-se onde os dois se encontram.
type ProfileRow struct {
	WeightKg  float64
	HeightCm  *float64
	Age       *int
	Sex       *string

	WorkoutDays    []int
	WorkoutMinutes int
	Experience     string
	Equipment      []string
	MealsPerDay    int
}

type ProfileRepo struct{ tx *TxManager }

func NewProfileRepo(tx *TxManager) *ProfileRepo { return &ProfileRepo{tx: tx} }

var ErrNoProfile = errors.New("perfil por preencher")

func (r *ProfileRepo) Profile(ctx context.Context, userID string) (ProfileRow, error) {
	var p ProfileRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT weight_kg, height_cm, age, sex::text,
		        workout_days, workout_minutes, experience::text, equipment, meals_per_day
		   FROM profile WHERE user_id = $1`, userID,
	).Scan(&p.WeightKg, &p.HeightCm, &p.Age, &p.Sex,
		&p.WorkoutDays, &p.WorkoutMinutes, &p.Experience, &p.Equipment, &p.MealsPerDay)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNoProfile
	}
	if err != nil {
		return p, fmt.Errorf("ler perfil: %w", err)
	}
	return p, nil
}

// PlanLabelOn devolve o rótulo do dia no plano, se houver plano.
func (r *ProfileRepo) PlanLabelOn(ctx context.Context, userID string, day time.Time) (string, bool) {
	var label string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT ps.label FROM planned_session ps
		   JOIN plan pl ON pl.id = ps.plan_id
		   JOIN journey j ON j.id = pl.journey_id
		   JOIN goal g ON g.id = j.goal_id
		  WHERE g.user_id = $1 AND ps.scheduled_on = $2
		  LIMIT 1`, userID, day).Scan(&label)
	return label, err == nil && label != ""
}
