package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// ErrProfileMissing e ErrWeightMissing atravessam a fronteira para o handler
// poder responder coisas diferentes: "ainda não criaste o perfil" e "falta
// dizer-nos o teu peso" não são o mesmo pedido a fazer a alguém.
var (
	ErrProfileMissing = repo.ErrNoProfile
	ErrWeightMissing  = repo.ErrNoWeight
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

// SaveProfileInput é o que a app manda no fim do onboarding.
type SaveProfileInput struct {
	DisplayName string
	BirthDate   *time.Time
	// AgeYears vale quando não há data de nascimento. Ver a migração 0005.
	AgeYears *int
	Sex      string
	HeightCm float64
	WeightKg *float64

	Experience     string
	WorkoutDays    []int
	WorkoutMinutes int
	WorkoutTime    string
	Equipment      []string

	DietStyle      string
	MealsPerDay    int
	FoodBudget     string
	FoodExclusions []string
}

// SavedProfile é o perfil como fica depois de gravado — o que a app desenha.
type SavedProfile struct {
	DisplayName    string   `json:"displayName"`
	Age            *int     `json:"age,omitempty"`
	Sex            string   `json:"sex"`
	HeightCm       *float64 `json:"heightCm,omitempty"`
	WeightKg       float64  `json:"weightKg,omitempty"`
	Experience     string   `json:"experience"`
	WorkoutDays    []int    `json:"workoutDays"`
	WorkoutMinutes int      `json:"workoutMinutes"`
	WorkoutTime    string   `json:"workoutTime"`
	Equipment      []string `json:"equipment"`
	DietStyle      string   `json:"dietStyle"`
	MealsPerDay    int      `json:"mealsPerDay"`
	FoodBudget     string   `json:"foodBudget"`
	FoodExclusions []string `json:"foodExclusions"`
	Complete       bool     `json:"complete"`
}

// Save grava o perfil e devolve-o como ficou.
//
// Devolver em vez de responder 204: a app desenha o que o servidor diz, e
// mandá-la pedir outra vez o que acabou de escrever é um ecrã em branco entre
// os dois pedidos.
func (p *Profiles) Save(ctx ctxLike, userID string, in SaveProfileInput, now time.Time) (SavedProfile, error) {
	c := asContext(ctx)
	if err := p.repo.Save(c, userID, repo.ProfileInput{
		DisplayName: in.DisplayName, BirthDate: in.BirthDate, AgeYears: in.AgeYears, Sex: in.Sex,
		HeightCm: in.HeightCm, WeightKg: in.WeightKg,
		Experience: in.Experience, WorkoutDays: in.WorkoutDays,
		WorkoutMinutes: in.WorkoutMinutes, WorkoutTime: in.WorkoutTime,
		Equipment: in.Equipment, DietStyle: in.DietStyle,
		MealsPerDay: in.MealsPerDay, FoodBudget: in.FoodBudget,
		FoodExclusions: in.FoodExclusions,
	}, now); err != nil {
		return SavedProfile{}, err
	}
	return p.Read(c, userID)
}

// Read devolve o perfil gravado. `ErrWeightMissing` não é impedimento: o perfil
// existe, só ainda não tem pesagem.
func (p *Profiles) Read(ctx ctxLike, userID string) (SavedProfile, error) {
	row, err := p.repo.Profile(asContext(ctx), userID)
	if err != nil && !errors.Is(err, repo.ErrNoWeight) {
		return SavedProfile{}, fmt.Errorf("ler perfil: %w", err)
	}
	sex := "unspecified"
	if row.Sex != nil {
		sex = *row.Sex
	}
	return SavedProfile{
		DisplayName: row.DisplayName, Age: row.Age, Sex: sex,
		HeightCm: row.HeightCm, WeightKg: row.WeightKg,
		Experience: row.Experience, WorkoutDays: nonNilInts(row.WorkoutDays),
		WorkoutMinutes: row.WorkoutMinutes, WorkoutTime: row.WorkoutTime,
		Equipment: nonNil(row.Equipment), DietStyle: row.DietStyle,
		MealsPerDay: row.MealsPerDay, FoodBudget: row.FoodBudget,
		FoodExclusions: nonNil(row.FoodExclusions), Complete: row.Complete,
	}, nil
}

// JSON com `null` onde a app espera uma lista faz `map` rebentar no cliente.
// Uma lista vazia é uma lista.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
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
	// Montar o treino de hoje não precisa de peso: precisa de experiência,
	// equipamento e tempo. Recusar por falta de uma pesagem seria fechar o
	// treino a quem ainda não se pesou — e o treino é o que traz a pessoa de
	// volta para se pesar.
	if err != nil && !errors.Is(err, repo.ErrNoWeight) {
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
