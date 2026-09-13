package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrGoalAlreadyActive corresponde ao código `goal_already_active` da API.
//
// Vem do índice único do esquema, não de uma consulta prévia: verificar antes e
// inserir depois é uma corrida que duas gravações simultâneas ganham as duas.
var ErrGoalAlreadyActive = errors.New("já existe um objetivo activo")

var ErrNotFound = errors.New("não encontrado")

type GoalRow struct {
	ID        string
	UserID    string
	Type      string
	Horizon   string
	Direction string
	Priority  string
	Status    string
}

type JourneyRow struct {
	ID         string
	GoalID     string
	Horizon    string
	StartDate  time.Time
	TargetDate *time.Time
	CycleWeeks *int
	Status     string
}

type PhaseRow struct {
	Kind      string
	Position  int
	StartDate time.Time
	EndDate   time.Time
}

type TargetRow struct {
	Metric    string
	Direction string
	Baseline  float64
	Value     float64
	Unit      string
	DueDate   *time.Time
}

type PlanRow struct {
	ID               string
	FrequencyPerWeek int
	SessionMinutes   int
	Intensity        string
	Progression      string
	Recovery         string
	EffectiveFrom    time.Time
}

type StrategyRow struct {
	Goal          string
	CalorieTarget int
	ProteinG      int
	CarbsG        int
	FatG          int
	TDEEEstimated int
}

type GoalRepo struct{ tx *TxManager }

func NewGoalRepo(tx *TxManager) *GoalRepo { return &GoalRepo{tx: tx} }

// isUniqueViolation distingue "já existe" de um erro real de base de dados.
// Sem isto, um conflito esperado sairia como 500.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		if constraint == "" {
			return true
		}
		var named interface{ Error() string }
		if errors.As(err, &named) {
			return containsFold(named.Error(), constraint)
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func (r *GoalRepo) InsertGoal(ctx context.Context, g GoalRow) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO goal (user_id, type, horizon, direction, priority, status)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		g.UserID, g.Type, g.Horizon, g.Direction, g.Priority, g.Status,
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err, "goal_one_active") {
			return "", ErrGoalAlreadyActive
		}
		return "", fmt.Errorf("gravar objetivo: %w", err)
	}
	return id, nil
}

func (r *GoalRepo) InsertJourney(ctx context.Context, j JourneyRow) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, target_date, cycle_weeks, status)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		j.GoalID, j.Horizon, j.StartDate, j.TargetDate, j.CycleWeeks, j.Status,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gravar jornada: %w", err)
	}
	return id, nil
}

func (r *GoalRepo) InsertPhases(ctx context.Context, journeyID string, phases []PhaseRow) error {
	if len(phases) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(phases))
	for _, p := range phases {
		rows = append(rows, []any{journeyID, p.Kind, p.Position, p.StartDate, p.EndDate})
	}
	return r.copyInto(ctx, "phase", []string{"journey_id", "kind", "position", "start_date", "end_date"}, rows)
}

func (r *GoalRepo) InsertCycle(ctx context.Context, journeyID string, index int, start, review time.Time) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO cycle (journey_id, index, start_date, review_date)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		journeyID, index, start, review,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("abrir ciclo: %w", err)
	}
	return id, nil
}

func (r *GoalRepo) InsertTargets(ctx context.Context, journeyID string, targets []TargetRow) error {
	if len(targets) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, []any{journeyID, t.Metric, t.Direction, t.Baseline, t.Value, t.Unit, t.DueDate})
	}
	return r.copyInto(ctx, "target",
		[]string{"journey_id", "metric", "direction", "baseline", "value", "unit", "due_date"}, rows)
}

func (r *GoalRepo) InsertPlan(ctx context.Context, journeyID string, phaseID, cycleID *string, p PlanRow) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO plan (journey_id, phase_id, cycle_id, frequency_per_week, session_minutes,
		                   intensity, progression, recovery, effective_from)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		journeyID, phaseID, cycleID, p.FrequencyPerWeek, p.SessionMinutes,
		p.Intensity, p.Progression, p.Recovery, p.EffectiveFrom,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gravar plano: %w", err)
	}
	return id, nil
}

func (r *GoalRepo) InsertStrategy(ctx context.Context, userID, journeyID string, s StrategyRow, from time.Time) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO nutrition_strategy (user_id, journey_id, goal, calorie_target,
		                                 protein_g, carbs_g, fat_g, tdee_estimated, effective_from)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		userID, journeyID, s.Goal, s.CalorieTarget, s.ProteinG, s.CarbsG, s.FatG, s.TDEEEstimated, from,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gravar estratégia: %w", err)
	}
	return id, nil
}

// CurrentStrategy é a estratégia nutricional em vigor no dia indicado.
//
// A estratégia é uma **decisão gravada**, não uma conta a refazer: o alvo
// calórico foi apresentado à pessoa quando o objectivo nasceu, e recalculá-lo a
// cada pedido faria o número mudar debaixo dela sempre que o peso ou os dias de
// treino mudassem um bocadinho.
//
// `effective_to` nulo quer dizer "ainda em vigor" — é o estado normal.
func (r *GoalRepo) CurrentStrategy(ctx context.Context, userID string, day time.Time) (StrategyRow, error) {
	var s StrategyRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT goal, calorie_target, protein_g, carbs_g, fat_g, tdee_estimated
		   FROM nutrition_strategy
		  WHERE user_id = $1
		    AND effective_from <= $2
		    AND (effective_to IS NULL OR effective_to >= $2)
		  ORDER BY effective_from DESC, created_at DESC
		  LIMIT 1`, userID, day,
	).Scan(&s.Goal, &s.CalorieTarget, &s.ProteinG, &s.CarbsG, &s.FatG, &s.TDEEEstimated)
	if errors.Is(err, pgx.ErrNoRows) {
		return StrategyRow{}, ErrNotFound
	}
	return s, err
}

// AppendEvent escreve no histórico imutável.
//
// Nunca é editado nem apagado: é dele que se reconstrói a razão pela qual o
// plano é o que é.
func (r *GoalRepo) AppendEvent(ctx context.Context, journeyID, kind string, payload any) error {
	blob := []byte("{}")
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("serializar evento: %w", err)
		}
		blob = b
	}
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO journey_event (journey_id, kind, payload) VALUES ($1, $2, $3)`,
		journeyID, kind, blob)
	if err != nil {
		return fmt.Errorf("gravar evento: %w", err)
	}
	return nil
}

// InsertAssessment guarda o instantâneo que produziu a decisão.
//
// Guardado inteiro, e com a versão da configuração lá dentro: sem isso, uma
// avaliação de há três meses deixa de ser explicável quando os limiares mudam.
func (r *GoalRepo) InsertAssessment(ctx context.Context, journeyID string, snapshot any, adherence float64, confidence string) error {
	blob, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("serializar avaliação: %w", err)
	}
	_, err = r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO assessment (journey_id, snapshot, adherence, confidence) VALUES ($1, $2, $3, $4)`,
		journeyID, blob, adherence, confidence)
	if err != nil {
		return fmt.Errorf("gravar avaliação: %w", err)
	}
	return nil
}

// CurrentGoal devolve o objetivo activo do utilizador, com a jornada.
func (r *GoalRepo) CurrentGoal(ctx context.Context, userID string) (GoalRow, JourneyRow, error) {
	var g GoalRow
	var j JourneyRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT g.id, g.user_id, g.type, g.horizon, g.direction, g.priority, g.status,
		        j.id, j.goal_id, j.horizon, j.start_date, j.target_date, j.cycle_weeks, j.status
		   FROM goal g
		   JOIN journey j ON j.goal_id = g.id AND j.status IN ('active','paused')
		  WHERE g.user_id = $1 AND g.status = 'active'
		  ORDER BY j.created_at DESC
		  LIMIT 1`, userID,
	).Scan(&g.ID, &g.UserID, &g.Type, &g.Horizon, &g.Direction, &g.Priority, &g.Status,
		&j.ID, &j.GoalID, &j.Horizon, &j.StartDate, &j.TargetDate, &j.CycleWeeks, &j.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, j, ErrNotFound
	}
	if err != nil {
		return g, j, fmt.Errorf("ler objetivo activo: %w", err)
	}
	return g, j, nil
}

// TargetsOf devolve o que a jornada se propõe medir.
//
// Separado do objetivo porque uma jornada pode ter vários alvos — o peso é o
// que a app mostra hoje, e `sessions_per_week` é o que um horizonte aberto usa
// em vez dele.
func (r *GoalRepo) TargetsOf(ctx context.Context, journeyID string) ([]TargetRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT metric::text, direction::text, baseline, value, unit, due_date
		   FROM target WHERE journey_id = $1 ORDER BY metric`, journeyID)
	if err != nil {
		return nil, fmt.Errorf("ler alvos: %w", err)
	}
	defer rows.Close()

	var out []TargetRow
	for rows.Next() {
		var t TargetRow
		if err := rows.Scan(&t.Metric, &t.Direction, &t.Baseline, &t.Value, &t.Unit, &t.DueDate); err != nil {
			return nil, fmt.Errorf("ler alvo: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *GoalRepo) PhasesOf(ctx context.Context, journeyID string) ([]PhaseRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT kind, position, start_date, end_date FROM phase
		  WHERE journey_id = $1 ORDER BY position`, journeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PhaseRow
	for rows.Next() {
		var p PhaseRow
		if err := rows.Scan(&p.Kind, &p.Position, &p.StartDate, &p.EndDate); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// copyInto usa COPY em vez de N inserts. Não é optimização prematura: a
// repartição de fases de uma jornada longa e as séries de uma sessão inteira
// entram em lote, e N viagens seriam N vezes a latência.
func (r *GoalRepo) copyInto(ctx context.Context, table string, cols []string, rows [][]any) error {
	q := r.tx.Q(ctx)
	type copier interface {
		CopyFrom(ctx context.Context, ident pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
	}
	if c, ok := q.(interface{ tx() pgx.Tx }); ok {
		_ = c
	}
	// pgx expõe CopyFrom no pool e na transacção, mas não na interface Querier.
	if cp, ok := any(q).(copier); ok {
		_, err := cp.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("copiar para %s: %w", table, err)
		}
		return nil
	}
	// Sem CopyFrom: inserção linha a linha, com o mesmo resultado.
	placeholders := ""
	for i := range cols {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += fmt.Sprintf("$%d", i+1)
	}
	names := ""
	for i, c := range cols {
		if i > 0 {
			names += ", "
		}
		names += c
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, names, placeholders)
	for _, row := range rows {
		if _, err := q.Exec(ctx, sql, row...); err != nil {
			return fmt.Errorf("inserir em %s: %w", table, err)
		}
	}
	return nil
}
