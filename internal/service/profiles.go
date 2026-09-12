package service

import (
	"context"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// Profiles traduz o perfil gravado no que os motores precisam.
//
// Vive aqui e não no repositório porque é o serviço que conhece os dois lados —
// e porque a tradução é uma decisão: sem plano ainda, o dia é de corpo inteiro,
// que é melhor do que não haver treino nenhum para mostrar.
type Profiles struct {
	repo *repo.ProfileRepo
	// NutritionGoal por omissão até o objectivo existir.
	defaultNutritionGoal string
}

func NewProfiles(r *repo.ProfileRepo) *Profiles {
	return &Profiles{repo: r, defaultNutritionGoal: "maintain"}
}

type ctxLike = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}

func asContext(ctx ctxLike) context.Context {
	if c, ok := ctx.(context.Context); ok {
		return c
	}
	return context.Background()
}

func (p *Profiles) Profile(ctx ctxLike, userID string) (CreateGoalInput, error) {
	row, err := p.repo.Profile(asContext(ctx), userID)
	if err != nil {
		return CreateGoalInput{}, err
	}
	return CreateGoalInput{
		UserID:          userID,
		CurrentWeightKg: row.WeightKg,
		HeightCm:        row.HeightCm,
		Age:             row.Age,
		Sex:             row.Sex,
		DaysPerWeek:     len(row.WorkoutDays),
		SessionMinutes:  row.WorkoutMinutes,
		Experience:      row.Experience,
		NutritionGoal:   p.defaultNutritionGoal,
		MealsPerDay:     row.MealsPerDay,
	}, nil
}

func (p *Profiles) TrainingProfile(ctx ctxLike, userID string, day time.Time) (TodayInput, error) {
	c := asContext(ctx)
	row, err := p.repo.Profile(c, userID)
	if err != nil {
		return TodayInput{}, err
	}

	label := "Full Body"
	if planned, ok := p.repo.PlanLabelOn(c, userID, day); ok {
		label = planned
	}
	return TodayInput{
		PlanLabel:      label,
		Experience:     row.Experience,
		Equipment:      row.Equipment,
		WorkoutMinutes: row.WorkoutMinutes,
		LocalDay:       day,
	}, nil
}
