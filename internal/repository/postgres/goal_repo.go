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

// LastAssessmentID é a avaliação mais recente de uma jornada.
//
// Existe porque `InsertAssessment` não devolve o id: a escrita e a leitura
// ficam a um passo uma da outra, e é a proposta de adaptação que precisa de os
// ligar — sem esse elo não há como explicar porque é que o plano mudou.
func (r *GoalRepo) LastAssessmentID(ctx context.Context, journeyID string) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id FROM assessment WHERE journey_id = $1
		  ORDER BY assessed_at DESC LIMIT 1`, journeyID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// ── Pausas ───────────────────────────────────────────────────────────────────
//
// Uma pausa não é abandonar: é dizer "não conto com isto esta semana". E tem de
// sair do denominador da adesão — sem isso, avisar que se vai estar fora sai
// mais caro do que desaparecer sem dizer nada, que é exactamente o incentivo
// errado.

// PauseJourney abre uma pausa. Abrir duas vezes não abre uma segunda.
func (r *GoalRepo) PauseJourney(ctx context.Context, userID, journeyID, reason string, at time.Time) (bool, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO journey_pause (journey_id, paused_at, reason)
		 SELECT j.id, $3, NULLIF($4,'')
		   FROM journey j JOIN goal g ON g.id = j.goal_id
		  WHERE j.id = $1 AND g.user_id = $2
		    AND NOT EXISTS (SELECT 1 FROM journey_pause p
		                     WHERE p.journey_id = j.id AND p.resumed_at IS NULL)
		 RETURNING id`, journeyID, userID, at, reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Ou a jornada não é desta pessoa, ou já estava em pausa. As duas
		// respondem o mesmo: não há nada a fazer.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pausar jornada: %w", err)
	}
	return true, nil
}

// ResumeJourney fecha a pausa aberta.
func (r *GoalRepo) ResumeJourney(ctx context.Context, userID, journeyID string, at time.Time) (bool, error) {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE journey_pause p SET resumed_at = $3
		   FROM journey j JOIN goal g ON g.id = j.goal_id
		  WHERE p.journey_id = j.id AND j.id = $1 AND g.user_id = $2
		    AND p.resumed_at IS NULL`, journeyID, userID, at)
	if err != nil {
		return false, fmt.Errorf("retomar jornada: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// PausedDays conta os dias em pausa dentro de um intervalo.
//
// Conta **dias inteiros** e nunca menos de zero: uma pausa de duas horas não
// tira um dia à conta, e uma pausa que ainda não fechou conta até hoje.
func (r *GoalRepo) PausedDays(ctx context.Context, journeyID string, from, to time.Time) (int, error) {
	var dias int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT COALESCE(SUM(
		          GREATEST(0, EXTRACT(EPOCH FROM (
		            LEAST(COALESCE(resumed_at, $3), $3) - GREATEST(paused_at, $2)
		          )) / 86400)::int
		        ), 0)
		   FROM journey_pause
		  WHERE journey_id = $1
		    AND paused_at < $3
		    AND (resumed_at IS NULL OR resumed_at > $2)`, journeyID, from, to).Scan(&dias)
	if err != nil {
		return 0, fmt.Errorf("contar dias de pausa: %w", err)
	}
	return dias, nil
}

// IsPaused diz se a jornada está em pausa agora.
func (r *GoalRepo) IsPaused(ctx context.Context, journeyID string) (bool, *time.Time, error) {
	var desde time.Time
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT paused_at FROM journey_pause
		  WHERE journey_id = $1 AND resumed_at IS NULL
		  ORDER BY paused_at DESC LIMIT 1`, journeyID).Scan(&desde)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, &desde, nil
}

/*
 * GoalOf traduz o objetivo activo para o nome que o produto usa.
 *
 * `strength | fatLoss | muscle | habit` — os mesmos quatro do assistente. O
 * esquema guarda tipo, direcção e prioridade porque é assim que o motor
 * raciocina; o produto fala em quatro palavras, e é por elas que uma playlist
 * é etiquetada.
 *
 * ⚠️ **A mesma regra existe no cliente**, em `mobile/lib/api/goal-map.ts`
 * (`goalDoServidor`). São duas cópias da mesma tradução, e isso é uma dívida:
 * mudar uma sem a outra faz a app dizer "ganhar massa" onde o servidor procura
 * playlists de "perder gordura". O caminho de saída é o servidor passar a
 * devolver este campo no objetivo e o cliente deixar de o derivar.
 */
func (r *GoalRepo) GoalOf(ctx context.Context, userID string) (string, error) {
	g, _, err := r.CurrentGoal(ctx, userID)
	if err != nil {
		return "", err
	}
	switch {
	case g.Priority == "performance":
		return "strength", nil
	case g.Type == "behavior" || g.Priority == "health":
		return "habit", nil
	case g.Direction == "gain_weight" || g.Priority == "muscle":
		return "muscle", nil
	default:
		return "fatLoss", nil
	}
}

// ErrHorizonteAberto diz que se tentou marcar data numa jornada sem ela.
var ErrHorizonteAberto = errors.New("jornada de horizonte aberto: não tem data para mudar")

// AlteracaoDeObjetivo é o que se pode mudar sem recomeçar a jornada.
//
// Nil quer dizer "não mexer". É a diferença entre um `PATCH` e um `PUT`: quem
// só muda a data não tem de reenviar o peso alvo, e um campo ausente não pode
// significar "apaga".
type AlteracaoDeObjetivo struct {
	Priority   *string
	TargetKg   *float64
	TargetDate *time.Time
}

/*
 * UpdateActiveGoal altera o objectivo activo.
 *
 * ⚠️ **Altera; não recomeça.** Mudar o peso alvo de 74 para 72 não devia
 * apagar três meses de histórico — e era essa a única saída que havia, porque o
 * `POST /v1/goals` recusa um segundo objectivo activo.
 *
 * O que não se altera por aqui: o tipo, a direcção e o **horizonte**. Trocar de
 * perder peso para ganhar massa não é corrigir um número. E passar de uma data
 * marcada para um horizonte aberto muda a forma da jornada inteira — o esquema
 * diz isso em voz alta: com data há fases e não há ciclos, sem data há ciclos e
 * não há fases (`journey_horizon_dates`). Isso é uma jornada nova, com a antiga
 * guardada.
 *
 * Devolve falso quando não há objectivo activo.
 */
func (r *GoalRepo) UpdateActiveGoal(ctx context.Context, userID string, a AlteracaoDeObjetivo) (bool, error) {
	q := r.tx.Q(ctx)

	var goalID, journeyID string
	err := q.QueryRow(ctx,
		`SELECT g.id::text, j.id::text
		   FROM goal g JOIN journey j ON j.goal_id = g.id
		  WHERE g.user_id = $1 AND g.status = 'active'`, userID).Scan(&goalID, &journeyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("ler objectivo activo: %w", err)
	}

	if a.Priority != nil {
		if _, err := q.Exec(ctx,
			`UPDATE goal SET priority = $2::goal_priority WHERE id = $1::uuid`,
			goalID, *a.Priority); err != nil {
			return false, fmt.Errorf("alterar prioridade: %w", err)
		}
	}

	if a.TargetDate != nil {
		// Só onde já havia data. Num horizonte aberto, marcar uma data é mudar
		// a forma da jornada — e a restrição `journey_horizon_dates` recusa-o,
		// com razão.
		tag, err := q.Exec(ctx,
			`UPDATE journey SET target_date = $2
			  WHERE id = $1::uuid AND horizon = 'fixed'`, journeyID, *a.TargetDate)
		if err != nil {
			return false, fmt.Errorf("alterar a data: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, ErrHorizonteAberto
		}
		if _, err := q.Exec(ctx,
			`UPDATE target SET due_date = $2 WHERE journey_id = $1::uuid`,
			journeyID, *a.TargetDate); err != nil {
			return false, fmt.Errorf("alterar a data dos alvos: %w", err)
		}
	}

	if a.TargetKg != nil {
		// O `baseline` não se toca: é de onde a pessoa partiu, e reescrevê-lo
		// apagava o progresso já feito.
		if _, err := q.Exec(ctx,
			`UPDATE target SET value = $2, status = 'pending'
			  WHERE journey_id = $1::uuid AND metric = 'body_weight'`,
			journeyID, *a.TargetKg); err != nil {
			return false, fmt.Errorf("alterar o peso alvo: %w", err)
		}
	}

	return true, nil
}
