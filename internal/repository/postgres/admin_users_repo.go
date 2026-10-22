package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminUsersRepo é o acompanhamento: as pessoas da app vistas pela equipa.
// Só leituras — o painel acompanha, não muda o plano de ninguém.
type AdminUsersRepo struct{ tx *TxManager }

func NewAdminUsersRepo(tx *TxManager) *AdminUsersRepo { return &AdminUsersRepo{tx: tx} }

type AdminUserRow struct {
	ID            string
	Name          string
	Phone         string
	PhotoURL      *string
	CreatedAt     time.Time
	LastSeenAt    *time.Time
	ProfileDone   bool
	Direction     *string
	JourneyStatus *string
	JourneyStart  *time.Time
	JourneyTarget *time.Time
	CycleWeeks    *int
	DaysPerWeek   int
	Done7         int
	LastWorkoutAt *time.Time
	TotalWorkouts int
	RiskLevel     *string
	RiskType      *string
	TeamIDs       []string
	TeamNames     []string
	TeamRoles     []string
	TeamInitials  []string
}

// Users devolve toda a gente que não apagou a conta, com o que a lista
// mostra. `specialist` corta às pessoas que convidaram esse especialista.
func (r *AdminUsersRepo) Users(ctx context.Context, specialist string, now time.Time) ([]AdminUserRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx, `
WITH gente AS (
  SELECT a.id, coalesce(p.display_name, '') AS name, a.phone_e164, p.photo_url, a.created_at, a.last_seen_at,
         coalesce(p.profile_complete, false) AS done,
         greatest(coalesce(array_length(p.workout_days, 1), 3), 1) AS dias
    FROM app_user a LEFT JOIN profile p ON p.user_id = a.id
   WHERE a.deleted_at IS NULL
     AND ($1 = '' OR EXISTS (SELECT 1 FROM user_specialist u WHERE u.user_id = a.id AND u.specialist_id = $1))
), jornada AS (
  SELECT DISTINCT ON (g.user_id) g.user_id, g.direction::text AS direction, j.id AS journey_id,
         j.status::text AS status, j.start_date, j.target_date, j.cycle_weeks
    FROM goal g JOIN journey j ON j.goal_id = g.id AND j.status IN ('active', 'paused')
   WHERE g.status = 'active'
   ORDER BY g.user_id, j.created_at DESC
), treino AS (
  SELECT user_id,
         count(*) FILTER (WHERE status = 'completed' AND occurred_at > $2::timestamptz - interval '7 days') AS feitos7,
         max(occurred_at) FILTER (WHERE status = 'completed') AS ultimo,
         count(*) FILTER (WHERE status = 'completed') AS total
    FROM workout_session GROUP BY user_id
), risco AS (
  SELECT DISTINCT ON (j.user_id) j.user_id, x->>'level' AS level, x->>'type' AS type
    FROM jornada j
    JOIN assessment s ON s.journey_id = j.journey_id AND s.assessed_at > $2::timestamptz - interval '14 days'
    CROSS JOIN LATERAL jsonb_array_elements(coalesce(s.snapshot->'risks', '[]'::jsonb)) x
   ORDER BY j.user_id,
            CASE x->>'level' WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END, s.assessed_at DESC
), equipa AS (
  SELECT u.user_id, array_agg(s.id ORDER BY s.role) AS ids, array_agg(s.name ORDER BY s.role) AS names,
         array_agg(s.role::text ORDER BY s.role) AS roles, array_agg(s.initials ORDER BY s.role) AS initials
    FROM user_specialist u JOIN specialist s ON s.id = u.specialist_id GROUP BY u.user_id
)
SELECT g.id, g.name, g.phone_e164, g.photo_url, g.created_at, g.last_seen_at, g.done,
       j.direction, j.status, j.start_date, j.target_date, j.cycle_weeks, g.dias,
       coalesce(t.feitos7, 0), t.ultimo, coalesce(t.total, 0), r.level, r.type,
       coalesce(e.ids, '{}'), coalesce(e.names, '{}'), coalesce(e.roles, '{}'), coalesce(e.initials, '{}')
  FROM gente g
  LEFT JOIN jornada j ON j.user_id = g.id
  LEFT JOIN treino t ON t.user_id = g.id
  LEFT JOIN risco r ON r.user_id = g.id
  LEFT JOIN equipa e ON e.user_id = g.id
 ORDER BY coalesce(g.last_seen_at, g.created_at) DESC`, specialist, now)
	if err != nil {
		return nil, fmt.Errorf("ler pessoas: %w", err)
	}
	defer rows.Close()
	out := []AdminUserRow{}
	for rows.Next() {
		var u AdminUserRow
		if err := rows.Scan(&u.ID, &u.Name, &u.Phone, &u.PhotoURL, &u.CreatedAt, &u.LastSeenAt, &u.ProfileDone,
			&u.Direction, &u.JourneyStatus, &u.JourneyStart, &u.JourneyTarget, &u.CycleWeeks, &u.DaysPerWeek,
			&u.Done7, &u.LastWorkoutAt, &u.TotalWorkouts, &u.RiskLevel, &u.RiskType,
			&u.TeamIDs, &u.TeamNames, &u.TeamRoles, &u.TeamInitials); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CanSee diz se o especialista pode abrir a ficha desta pessoa.
func (r *AdminUsersRepo) CanSee(ctx context.Context, specialist, userID string) (bool, error) {
	var ok bool
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_specialist WHERE specialist_id = $1 AND user_id = $2)`,
		specialist, userID).Scan(&ok)
	return ok, err
}

// ── A ficha ──────────────────────────────────────────────────────────────────

type AdminRecordRow struct {
	ID         string
	Name       string
	Phone      string
	PhotoURL   *string
	CreatedAt  time.Time
	LastSeenAt *time.Time
	Profile    map[string]any
	Weights    []DayValue
	Training   []DayStatus
	Food       []DayFood
	Sessions   []RecordSession
	Adapt      []RecordAdaptation
	Strategy   *RecordStrategy
	Team       []RecordTeam
	Direction  *string
	JourneyID  *string
	Start      *time.Time
	Target     *time.Time
	CycleWeeks *int
	Status     *string
}

type DayValue struct {
	Day   time.Time
	Value float64
}
type DayStatus struct {
	Day    time.Time
	Status string
}
type DayFood struct {
	Day  time.Time
	Logs int
	Kcal int
}
type RecordSession struct {
	ID, Title, Focus, Status        string
	Day                             time.Time
	Duration, Planned, Done, SetsPl int
	Kcal                            int
}
type RecordAdaptation struct {
	ID, Kind  string
	Payload   map[string]any
	CreatedAt time.Time
	Status    string
}
type RecordStrategy struct {
	Kcal, Protein, Carbs, Fat, TDEE int
}
type RecordTeam struct {
	ID, Name, Initials, Role string
	InvitedAt                time.Time
}

func (r *AdminUsersRepo) Record(ctx context.Context, userID string, now time.Time) (AdminRecordRow, error) {
	q := r.tx.Q(ctx)
	var rec AdminRecordRow
	var profile []byte
	err := q.QueryRow(ctx,
		`SELECT a.id, coalesce(p.display_name, ''), a.phone_e164, p.photo_url, a.created_at, a.last_seen_at,
		        CASE WHEN p.user_id IS NULL THEN NULL ELSE jsonb_build_object(
		          'sex', p.sex, 'age', p.age_years, 'heightCm', p.height_cm, 'experience', p.experience,
		          'workoutDays', p.workout_days, 'workoutMinutes', p.workout_minutes,
		          'workoutTime', p.workout_time, 'equipment', p.equipment, 'dietStyle', p.diet_style,
		          'mealsPerDay', p.meals_per_day, 'foodBudget', p.food_budget,
		          'foodExclusions', p.food_exclusions, 'maxImpact', p.max_impact,
		          'nutritionDetail', p.nutrition_detail) END
		   FROM app_user a LEFT JOIN profile p ON p.user_id = a.id
		  WHERE a.id = $1 AND a.deleted_at IS NULL`, userID).
		Scan(&rec.ID, &rec.Name, &rec.Phone, &rec.PhotoURL, &rec.CreatedAt, &rec.LastSeenAt, &profile)
	if errors.Is(err, pgx.ErrNoRows) {
		return rec, ErrNotFound
	}
	if err != nil {
		return rec, err
	}
	if profile != nil {
		_ = json.Unmarshal(profile, &rec.Profile)
	}

	_ = q.QueryRow(ctx,
		`SELECT g.direction::text, j.id::text, j.start_date, j.target_date, j.cycle_weeks, j.status::text
		   FROM goal g JOIN journey j ON j.goal_id = g.id AND j.status IN ('active','paused')
		  WHERE g.user_id = $1 AND g.status = 'active' ORDER BY j.created_at DESC LIMIT 1`, userID).
		Scan(&rec.Direction, &rec.JourneyID, &rec.Start, &rec.Target, &rec.CycleWeeks, &rec.Status)

	desde := now.AddDate(0, 0, -120)
	rows, err := q.Query(ctx,
		`SELECT DISTINCT ON (recorded_at::date) recorded_at::date, value::float8
		   FROM measurement WHERE user_id = $1 AND metric = 'body_weight' AND recorded_at > $2
		  ORDER BY recorded_at::date, recorded_at DESC`, userID, desde)
	if err != nil {
		return rec, err
	}
	for rows.Next() {
		var d DayValue
		if err := rows.Scan(&d.Day, &d.Value); err != nil {
			rows.Close()
			return rec, err
		}
		rec.Weights = append(rec.Weights, d)
	}
	rows.Close()

	cinco := now.AddDate(0, 0, -35)
	rows, err = q.Query(ctx,
		`SELECT local_day, CASE WHEN bool_or(status = 'completed') THEN 'done' ELSE 'skipped' END
		   FROM workout_session WHERE user_id = $1 AND local_day > $2::date GROUP BY local_day`, userID, cinco)
	if err != nil {
		return rec, err
	}
	for rows.Next() {
		var d DayStatus
		if err := rows.Scan(&d.Day, &d.Status); err != nil {
			rows.Close()
			return rec, err
		}
		rec.Training = append(rec.Training, d)
	}
	rows.Close()

	rows, err = q.Query(ctx,
		`SELECT local_day, count(*) FILTER (WHERE status <> 'skipped'), coalesce(sum(kcal) FILTER (WHERE status <> 'skipped'), 0)
		   FROM nutrition_log WHERE user_id = $1 AND local_day > $2::date GROUP BY local_day`, userID, cinco)
	if err != nil {
		return rec, err
	}
	for rows.Next() {
		var d DayFood
		if err := rows.Scan(&d.Day, &d.Logs, &d.Kcal); err != nil {
			rows.Close()
			return rec, err
		}
		rec.Food = append(rec.Food, d)
	}
	rows.Close()

	rows, err = q.Query(ctx,
		`SELECT id::text, title, focus::text, status::text, local_day, duration_seconds, planned_seconds,
		        sets_done, sets_planned, kcal
		   FROM workout_session WHERE user_id = $1 ORDER BY occurred_at DESC LIMIT 10`, userID)
	if err != nil {
		return rec, err
	}
	for rows.Next() {
		var s RecordSession
		if err := rows.Scan(&s.ID, &s.Title, &s.Focus, &s.Status, &s.Day, &s.Duration, &s.Planned,
			&s.Done, &s.SetsPl, &s.Kcal); err != nil {
			rows.Close()
			return rec, err
		}
		rec.Sessions = append(rec.Sessions, s)
	}
	rows.Close()

	if rec.JourneyID != nil {
		rows, err = q.Query(ctx,
			`SELECT a.id::text, a.kind::text, a.payload, s.assessed_at,
			        CASE WHEN a.applied_at IS NOT NULL THEN 'applied'
			             WHEN a.dismissed_at IS NOT NULL THEN 'dismissed' ELSE 'pending' END
			   FROM adaptation a JOIN assessment s ON s.id = a.assessment_id
			  WHERE a.journey_id = $1 ORDER BY s.assessed_at DESC LIMIT 10`, *rec.JourneyID)
		if err != nil {
			return rec, err
		}
		for rows.Next() {
			var a RecordAdaptation
			var payload []byte
			if err := rows.Scan(&a.ID, &a.Kind, &payload, &a.CreatedAt, &a.Status); err != nil {
				rows.Close()
				return rec, err
			}
			_ = json.Unmarshal(payload, &a.Payload)
			rec.Adapt = append(rec.Adapt, a)
		}
		rows.Close()
	}

	var st RecordStrategy
	err = q.QueryRow(ctx,
		`SELECT calorie_target, protein_g, carbs_g, fat_g, tdee_estimated FROM nutrition_strategy
		  WHERE user_id = $1 AND effective_to IS NULL ORDER BY effective_from DESC LIMIT 1`, userID).
		Scan(&st.Kcal, &st.Protein, &st.Carbs, &st.Fat, &st.TDEE)
	if err == nil {
		rec.Strategy = &st
	}

	rows, err = q.Query(ctx,
		`SELECT s.id, s.name, s.initials, s.role::text, u.invited_at
		   FROM user_specialist u JOIN specialist s ON s.id = u.specialist_id
		  WHERE u.user_id = $1 ORDER BY s.role`, userID)
	if err != nil {
		return rec, err
	}
	defer rows.Close()
	for rows.Next() {
		var t RecordTeam
		if err := rows.Scan(&t.ID, &t.Name, &t.Initials, &t.Role, &t.InvitedAt); err != nil {
			return rec, err
		}
		rec.Team = append(rec.Team, t)
	}
	return rec, rows.Err()
}

// ── Visão geral ──────────────────────────────────────────────────────────────

type OverviewRow struct {
	Active, ActivePrev     int
	Workouts, WorkoutsPrev int
	Logs, LogsPrev         int
	Total                  int
	ByDay                  []DayCount
	Funnel                 [5]int
	AvgAdherence           *float64
	AvgAdherencePrev       *float64
	HighRisk               int
	HighRiskNames          []string
	HighRiskIDs            []string
	ClassFailed            int
	ClassWaiting           int
	ClassReadyUnpublished  int
	RecipesToValidate      int
	DietsInReview          int
	AISpentMicro           int64
	AIRuns                 int
	AIApplied, AICorrected int
	AIDismissed, AIFailed  int
}

type DayCount struct {
	Day    time.Time
	People int
}

func (r *AdminUsersRepo) Overview(ctx context.Context, days int, now time.Time) (OverviewRow, error) {
	q := r.tx.Q(ctx)
	var o OverviewRow
	from := now.AddDate(0, 0, -days)
	prev := from.AddDate(0, 0, -days)

	activos := `SELECT count(DISTINCT user_id) FROM (
		SELECT user_id FROM workout_session WHERE occurred_at >= $1 AND occurred_at < $2
		UNION SELECT user_id FROM nutrition_log WHERE recorded_at >= $1 AND recorded_at < $2
		UNION SELECT user_id FROM measurement WHERE recorded_at >= $1 AND recorded_at < $2) x`
	if err := q.QueryRow(ctx, activos, from, now).Scan(&o.Active); err != nil {
		return o, err
	}
	if err := q.QueryRow(ctx, activos, prev, from).Scan(&o.ActivePrev); err != nil {
		return o, err
	}
	treinos := `SELECT count(*) FROM workout_session WHERE status = 'completed' AND occurred_at >= $1 AND occurred_at < $2`
	_ = q.QueryRow(ctx, treinos, from, now).Scan(&o.Workouts)
	_ = q.QueryRow(ctx, treinos, prev, from).Scan(&o.WorkoutsPrev)
	registos := `SELECT count(*) FROM nutrition_log WHERE status <> 'skipped' AND recorded_at >= $1 AND recorded_at < $2`
	_ = q.QueryRow(ctx, registos, from, now).Scan(&o.Logs)
	_ = q.QueryRow(ctx, registos, prev, from).Scan(&o.LogsPrev)
	_ = q.QueryRow(ctx, `SELECT count(*) FROM app_user WHERE deleted_at IS NULL`).Scan(&o.Total)

	// A adesão média: treinos feitos sobre os combinados no perfil, por quem
	// tem uma jornada activa — limitada a 100%, para um entusiasta não
	// esconder três desistentes.
	adesao := `
	  SELECT avg(least(1.0, coalesce(t.n, 0)::float8 /
	             greatest(1, coalesce(array_length(p.workout_days, 1), 3) * $3::float8 / 7)))
	    FROM goal g JOIN journey j ON j.goal_id = g.id AND j.status = 'active'
	    JOIN profile p ON p.user_id = g.user_id
	    JOIN app_user a ON a.id = g.user_id AND a.deleted_at IS NULL
	    LEFT JOIN (SELECT user_id, count(*) n FROM workout_session
	                WHERE status = 'completed' AND occurred_at >= $1 AND occurred_at < $2 GROUP BY user_id) t
	      ON t.user_id = g.user_id
	   WHERE g.status = 'active' AND j.start_date <= $1::date`
	_ = q.QueryRow(ctx, adesao, from, now, days).Scan(&o.AvgAdherence)
	_ = q.QueryRow(ctx, adesao, prev, from, days).Scan(&o.AvgAdherencePrev)

	// Dois períodos: o actual e o anterior, que o gráfico desenha mais claro.
	rows, err := q.Query(ctx, `
	  SELECT d::date, count(DISTINCT x.user_id)
	    FROM generate_series($1::date, $2::date, interval '1 day') d
	    LEFT JOIN (SELECT user_id, local_day FROM workout_session
	               UNION ALL SELECT user_id, local_day FROM nutrition_log) x ON x.local_day = d::date
	   GROUP BY d ORDER BY d`, prev.AddDate(0, 0, 1), now)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.People); err != nil {
			rows.Close()
			return o, err
		}
		o.ByDay = append(o.ByDay, d)
	}
	rows.Close()

	// O percurso de quem entrou nos últimos 90 dias.
	_ = q.QueryRow(ctx, `
	  WITH c AS (SELECT a.id, a.created_at FROM app_user a
	              WHERE a.deleted_at IS NULL AND a.created_at > $1::timestamptz - interval '90 days')
	  SELECT count(*),
	         count(*) FILTER (WHERE EXISTS (SELECT 1 FROM profile p WHERE p.user_id = c.id AND p.profile_complete)),
	         count(*) FILTER (WHERE EXISTS (SELECT 1 FROM goal g WHERE g.user_id = c.id)),
	         count(*) FILTER (WHERE EXISTS (SELECT 1 FROM workout_session w WHERE w.user_id = c.id AND w.status = 'completed')),
	         count(*) FILTER (WHERE EXISTS (SELECT 1 FROM workout_session w WHERE w.user_id = c.id AND w.status = 'completed'
	                                        AND w.occurred_at >= c.created_at + interval '7 days'
	                                        AND w.occurred_at < c.created_at + interval '14 days'))
	    FROM c`, now).Scan(&o.Funnel[0], &o.Funnel[1], &o.Funnel[2], &o.Funnel[3], &o.Funnel[4])

	rows, err = q.Query(ctx, `
	  SELECT DISTINCT ON (g.user_id) g.user_id::text, coalesce(p.display_name, '')
	    FROM goal g JOIN journey j ON j.goal_id = g.id AND j.status = 'active'
	    JOIN assessment s ON s.journey_id = j.id AND s.assessed_at > $1::timestamptz - interval '7 days'
	    CROSS JOIN LATERAL jsonb_array_elements(coalesce(s.snapshot->'risks','[]'::jsonb)) x
	    LEFT JOIN profile p ON p.user_id = g.user_id
	   WHERE g.status = 'active' AND x->>'level' = 'high'`, now)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return o, err
		}
		o.HighRisk++
		o.HighRiskIDs = append(o.HighRiskIDs, id)
		o.HighRiskNames = append(o.HighRiskNames, name)
	}
	rows.Close()

	_ = q.QueryRow(ctx, `
	  SELECT count(*) FILTER (WHERE video_status = 'failed'),
	         count(*) FILTER (WHERE video_status = 'waiting' AND admin_edited_at < $1::timestamptz - interval '1 hour'),
	         count(*) FILTER (WHERE NOT published AND mux_ready)
	    FROM workout_class`, now).Scan(&o.ClassFailed, &o.ClassWaiting, &o.ClassReadyUnpublished)
	_ = q.QueryRow(ctx, `SELECT count(*) FROM recipe WHERE active AND validated_at IS NULL`).Scan(&o.RecipesToValidate)
	_ = q.QueryRow(ctx, `SELECT count(*) FROM diet_model WHERE status = 'review'`).Scan(&o.DietsInReview)

	mes := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	_ = q.QueryRow(ctx, `SELECT coalesce(sum(cost_micro_usd), 0), count(*) FROM ai_usage WHERE created_at >= $1`, mes).
		Scan(&o.AISpentMicro, &o.AIRuns)
	// Confirmadas tal como vieram, e as que a pessoa corrigiu antes — a
	// medida de quanto a IA acerta.
	_ = q.QueryRow(ctx, `
	  SELECT count(*) FILTER (WHERE kind = 'proposal_applied' AND NOT coalesce((detail->>'editedByPerson')::boolean, false)),
	         count(*) FILTER (WHERE kind = 'proposal_applied' AND coalesce((detail->>'editedByPerson')::boolean, false)),
	         count(*) FILTER (WHERE kind = 'proposal_dismissed')
	    FROM ai_audit_event WHERE created_at >= $1`, mes).Scan(&o.AIApplied, &o.AICorrected, &o.AIDismissed)
	_ = q.QueryRow(ctx, `SELECT count(*) FROM ai_job WHERE status = 'failed' AND created_at >= $1`, mes).Scan(&o.AIFailed)
	return o, nil
}
