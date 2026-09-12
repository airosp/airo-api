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
//
// ⚠️ **O peso não está aqui como coluna.** É uma série em `measurement`, e o que
// este tipo traz é a última leitura. Tratá-lo como campo do perfil foi o que
// tornou impossível calcular tendência — está escrito no esquema e vale a pena
// repeti-lo aqui, porque a tentação volta sempre que alguém quer "só o peso".
type ProfileRow struct {
	DisplayName string
	WeightKg    float64
	HeightCm    *float64
	Age         *int
	Sex         *string

	WorkoutDays    []int
	WorkoutMinutes int
	WorkoutTime    string
	Experience     string
	Equipment      []string

	DietStyle      string
	MealsPerDay    int
	FoodBudget     string
	FoodExclusions []string

	Complete bool
}

// ProfileInput é o que se grava. Separado da leitura porque os dois não são a
// mesma coisa: a leitura traz o peso da série, a escrita acrescenta-lhe um
// ponto.
type ProfileInput struct {
	DisplayName string
	BirthDate   *time.Time
	Sex         string
	HeightCm    float64

	Experience     string
	WorkoutDays    []int
	WorkoutMinutes int
	WorkoutTime    string
	Equipment      []string

	DietStyle      string
	MealsPerDay    int
	FoodBudget     string
	FoodExclusions []string

	// WeightKg, quando presente, entra como medição — não como campo.
	WeightKg *float64
}

type ProfileRepo struct{ tx *TxManager }

func NewProfileRepo(tx *TxManager) *ProfileRepo { return &ProfileRepo{tx: tx} }

var (
	ErrNoProfile = errors.New("perfil por preencher")
	// ErrNoWeight é o perfil que existe mas nunca teve uma pesagem. Os motores
	// precisam de peso para decidir seja o que for, e adivinhar um seria pior
	// do que dizer que falta.
	ErrNoWeight = errors.New("sem peso registado")
)

func (r *ProfileRepo) Profile(ctx context.Context, userID string) (ProfileRow, error) {
	var p ProfileRow
	var birth *time.Time

	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT display_name, birth_date, sex::text, height_cm,
		        experience::text, workout_days, workout_minutes, workout_time::text,
		        equipment, diet_style::text, meals_per_day, food_budget::text,
		        food_exclusions, profile_complete
		   FROM profile WHERE user_id = $1`, userID,
	).Scan(&p.DisplayName, &birth, &p.Sex, &p.HeightCm,
		&p.Experience, &p.WorkoutDays, &p.WorkoutMinutes, &p.WorkoutTime,
		&p.Equipment, &p.DietStyle, &p.MealsPerDay, &p.FoodBudget,
		&p.FoodExclusions, &p.Complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNoProfile
	}
	if err != nil {
		return p, fmt.Errorf("ler perfil: %w", err)
	}

	if birth != nil {
		age := yearsSince(*birth, time.Now().UTC())
		p.Age = &age
	}

	// A última pesagem. `ORDER BY recorded_at DESC` e não `created_at`: quem
	// regista ontem uma pesagem de ontem não muda o peso de hoje.
	err = r.tx.Q(ctx).QueryRow(ctx,
		`SELECT value FROM measurement
		  WHERE user_id = $1 AND metric = 'body_weight'
		  ORDER BY recorded_at DESC LIMIT 1`, userID).Scan(&p.WeightKg)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNoWeight
	}
	if err != nil {
		return p, fmt.Errorf("ler peso: %w", err)
	}

	return p, nil
}

// Save grava o perfil e, se vier peso, acrescenta-lhe uma medição.
//
// Um `INSERT ... ON CONFLICT` em vez de ler-e-decidir: dois pedidos do mesmo
// telemóvel a chegarem juntos davam duas linhas ou um erro, conforme a sorte.
func (r *ProfileRepo) Save(ctx context.Context, userID string, in ProfileInput, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO profile (
		     user_id, display_name, birth_date, sex, height_cm,
		     experience, workout_days, workout_minutes, workout_time, equipment,
		     diet_style, meals_per_day, food_budget, food_exclusions,
		     profile_complete, updated_at)
		 VALUES ($1,$2,$3,$4::sex,$5,$6::experience,$7,$8,$9::workout_time,$10,
		         $11::diet_style,$12,$13::budget,$14,true,$15)
		 ON CONFLICT (user_id) DO UPDATE SET
		     display_name = EXCLUDED.display_name,
		     birth_date = EXCLUDED.birth_date,
		     sex = EXCLUDED.sex,
		     height_cm = EXCLUDED.height_cm,
		     experience = EXCLUDED.experience,
		     workout_days = EXCLUDED.workout_days,
		     workout_minutes = EXCLUDED.workout_minutes,
		     workout_time = EXCLUDED.workout_time,
		     equipment = EXCLUDED.equipment,
		     diet_style = EXCLUDED.diet_style,
		     meals_per_day = EXCLUDED.meals_per_day,
		     food_budget = EXCLUDED.food_budget,
		     food_exclusions = EXCLUDED.food_exclusions,
		     profile_complete = true,
		     updated_at = EXCLUDED.updated_at`,
		userID, in.DisplayName, in.BirthDate, in.Sex, in.HeightCm,
		in.Experience, in.WorkoutDays, in.WorkoutMinutes, in.WorkoutTime, in.Equipment,
		in.DietStyle, in.MealsPerDay, in.FoodBudget, in.FoodExclusions, now)
	if err != nil {
		return fmt.Errorf("gravar perfil: %w", err)
	}

	if in.WeightKg != nil {
		if err := r.RecordWeight(ctx, userID, *in.WeightKg, now); err != nil {
			return err
		}
	}
	return nil
}

// RecordWeight acrescenta um ponto à série do peso.
func (r *ProfileRepo) RecordWeight(ctx context.Context, userID string, kg float64, at time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO measurement (user_id, metric, value, unit, recorded_at, source)
		 VALUES ($1, 'body_weight', $2, 'kg', $3, 'manual')`,
		userID, kg, at)
	if err != nil {
		return fmt.Errorf("gravar peso: %w", err)
	}
	return nil
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

// yearsSince conta anos completos. Faz-se à mão porque dividir dias por 365,25
// erra no dia do aniversário — e é exactamente nesse dia que alguém repara.
func yearsSince(birth, now time.Time) int {
	years := now.Year() - birth.Year()
	if now.YearDay() < birth.YearDay() {
		years--
	}
	if years < 0 {
		years = 0
	}
	return years
}
