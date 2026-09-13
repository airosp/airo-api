package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/domain"
	"github.com/jackc/pgx/v5"
)

type SessionRow struct {
	ID        string
	UserID    string
	JourneyID *string

	Title  string
	Focus  string
	Status string

	OccurredAt time.Time
	LocalDay   time.Time

	PlannedSeconds  int
	DurationSeconds int
	SetsPlanned     int
	SetsDone        int
	Kcal            int
	ExerciseCount   int

	WarmupSeconds   *int
	MainSeconds     *int
	CooldownSeconds *int

	IdempotencyKey *string
}

type PrescriptionRow struct {
	ExerciseSlug string
	Position     int
	Role         string
	Sets         int
	Target       domain.Metric
	RestSeconds  int

	// Detail é o que aconteceu em cada série. Vazio quando o cliente ainda não
	// o recolhe — e nesse caso a tabela fica sem detalhe em vez de ficar com
	// `actual = target`, que seria o mesmo que não a ter.
	Detail []SetRow
}

type SetRow struct {
	Index     int
	Target    domain.Metric
	Actual    *domain.Metric
	Completed bool
}

type SessionRepo struct {
	tx      *TxManager
	catalog *CatalogRepo
}

func NewSessionRepo(tx *TxManager, catalog *CatalogRepo) *SessionRepo {
	return &SessionRepo{tx: tx, catalog: catalog}
}

// FindByIdempotencyKey devolve a sessão já gravada com esta chave.
//
// É o que torna o reenvio seguro: a resposta é `200` com o registo existente, e
// não um segundo treino.
func (r *SessionRepo) FindByIdempotencyKey(ctx context.Context, userID, key string) (SessionRow, bool, error) {
	var s SessionRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id, status, sets_done, duration_seconds FROM workout_session
		  WHERE user_id = $1 AND idempotency_key = $2`, userID, key,
	).Scan(&s.ID, &s.Status, &s.SetsDone, &s.DurationSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, fmt.Errorf("procurar sessão por chave: %w", err)
	}
	s.UserID = userID
	return s, true, nil
}

// Insert grava a sessão com as suas prescrições e séries.
//
// Quatro tabelas numa transacção: ou entra tudo, ou nada. Uma sessão sem as suas
// séries é pior do que nenhuma sessão — o histórico diria que houve treino e não
// saberia dizer qual.
func (r *SessionRepo) Insert(ctx context.Context, s SessionRow, prescriptions []PrescriptionRow) (string, error) {
	var sessionID string

	err := r.tx.Do(ctx, func(ctx context.Context) error {
		q := r.tx.Q(ctx)
		err := q.QueryRow(ctx,
			`INSERT INTO workout_session
			   (user_id, journey_id, title, focus, status, occurred_at, local_day,
			    planned_seconds, duration_seconds, sets_planned, sets_done, kcal, exercise_count,
			    warmup_seconds, main_seconds, cooldown_seconds, idempotency_key)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			 RETURNING id`,
			s.UserID, s.JourneyID, s.Title, s.Focus, s.Status, s.OccurredAt, s.LocalDay,
			s.PlannedSeconds, s.DurationSeconds, s.SetsPlanned, s.SetsDone, s.Kcal, s.ExerciseCount,
			s.WarmupSeconds, s.MainSeconds, s.CooldownSeconds, s.IdempotencyKey,
		).Scan(&sessionID)
		if err != nil {
			if isUniqueViolation(err, "workout_session_idem") {
				return ErrDuplicateSession
			}
			return fmt.Errorf("gravar sessão: %w", err)
		}

		if len(prescriptions) == 0 {
			return nil
		}

		slugs := make([]string, 0, len(prescriptions))
		for _, p := range prescriptions {
			slugs = append(slugs, p.ExerciseSlug)
		}
		ids, err := r.catalog.ExerciseIDs(ctx, slugs)
		if err != nil {
			return err
		}

		for _, p := range prescriptions {
			exerciseID, ok := ids[p.ExerciseSlug]
			if !ok {
				// Um exercício que não está no catálogo é um erro de dados, não
				// um caso a ignorar em silêncio: a sessão ficaria incompleta e
				// ninguém saberia porquê.
				return fmt.Errorf("exercício %q não existe no catálogo", p.ExerciseSlug)
			}
			target, err := json.Marshal(p.Target)
			if err != nil {
				return err
			}

			var prescriptionID string
			err = q.QueryRow(ctx,
				`INSERT INTO exercise_prescription
				   (session_id, exercise_id, position, role, sets, target, rest_seconds)
				 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
				sessionID, exerciseID, p.Position, p.Role, p.Sets, target, p.RestSeconds,
			).Scan(&prescriptionID)
			if err != nil {
				return fmt.Errorf("gravar prescrição %d: %w", p.Position, err)
			}

			for _, set := range p.Detail {
				setTarget, err := json.Marshal(set.Target)
				if err != nil {
					return err
				}
				var actual any
				if set.Actual != nil {
					b, err := json.Marshal(set.Actual)
					if err != nil {
						return err
					}
					actual = b
				}
				var completedAt any
				if set.Completed {
					completedAt = s.OccurredAt
				}
				if _, err := q.Exec(ctx,
					`INSERT INTO exercise_set (prescription_id, index, target, actual, completed, completed_at)
					 VALUES ($1,$2,$3,$4,$5,$6)`,
					prescriptionID, set.Index, setTarget, actual, set.Completed, completedAt); err != nil {
					return fmt.Errorf("gravar série %d: %w", set.Index, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// ErrDuplicateSession corresponde ao código `session_already_recorded`.
var ErrDuplicateSession = errors.New("sessão já registada")

// Streak conta dias seguidos com uma sessão **concluída**, a contar de hoje.
//
// Uma sessão saltada não conta para a sequência, mas fica gravada: é a distinção
// que a adesão precisa e que a sequência não deve fazer.
// HistoryRow é uma sessão como o histórico a mostra.
//
// Sem as prescrições: o histórico desenha o que aconteceu — quanto tempo, que
// séries, em que blocos — e não o que estava prescrito. Trazê-las seria
// multiplicar as linhas por exercício para não desenhar nenhuma.
type HistoryRow struct {
	ID              string
	IdempotencyKey  *string
	Title           string
	Focus           string
	Status          string
	OccurredAt      time.Time
	LocalDay        time.Time
	PlannedSeconds  int
	DurationSeconds int
	SetsPlanned     int
	SetsDone        int
	Kcal            int
	ExerciseCount   int
	WarmupSeconds   *int
	MainSeconds     *int
	CooldownSeconds *int
}

// History devolve as sessões de um intervalo de dias, inclusive.
//
// Pela ordem em que aconteceram, da mais recente para a mais antiga: é a ordem
// em que o histórico se lê, e é a do índice que já existe.
func (r *SessionRepo) History(ctx context.Context, userID string, from, to time.Time) ([]HistoryRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, idempotency_key, title, focus::text, status::text,
		        occurred_at, local_day, planned_seconds, duration_seconds,
		        sets_planned, sets_done, kcal, exercise_count,
		        warmup_seconds, main_seconds, cooldown_seconds
		   FROM workout_session
		  WHERE user_id = $1 AND local_day BETWEEN $2 AND $3
		  ORDER BY occurred_at DESC`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("ler histórico: %w", err)
	}
	defer rows.Close()

	var out []HistoryRow
	for rows.Next() {
		var h HistoryRow
		if err := rows.Scan(&h.ID, &h.IdempotencyKey, &h.Title, &h.Focus, &h.Status,
			&h.OccurredAt, &h.LocalDay, &h.PlannedSeconds, &h.DurationSeconds,
			&h.SetsPlanned, &h.SetsDone, &h.Kcal, &h.ExerciseCount,
			&h.WarmupSeconds, &h.MainSeconds, &h.CooldownSeconds); err != nil {
			return nil, fmt.Errorf("ler sessão: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *SessionRepo) Streak(ctx context.Context, userID string, today time.Time) (int, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT DISTINCT local_day FROM workout_session
		  WHERE user_id = $1 AND status = 'completed' AND local_day <= $2
		  ORDER BY local_day DESC LIMIT 400`, userID, today)
	if err != nil {
		return 0, fmt.Errorf("ler sequência: %w", err)
	}
	defer rows.Close()

	streak := 0
	expected := today
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			return 0, err
		}
		switch {
		case sameDay(day, expected):
			streak++
			expected = expected.AddDate(0, 0, -1)
		case streak == 0 && sameDay(day, expected.AddDate(0, 0, -1)):
			// Ainda não treinou hoje: a sequência de ontem continua viva até ao
			// fim do dia. Quebrá-la à meia-noite seria castigar quem treina à
			// noite.
			streak++
			expected = day.AddDate(0, 0, -1)
		default:
			return streak, rows.Err()
		}
	}
	return streak, rows.Err()
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// ── Eventos da sessão ────────────────────────────────────────────────────────

type EventRow struct {
	Kind       string
	StepIndex  *int
	Payload    map[string]any
	OccurredAt time.Time
}

// AppendEvents grava um lote, ignorando os que já lá estavam.
//
// Devolve quantos são novos. O cliente pode reenviar o mesmo lote — é o que
// acontece quando a rede cai a meio do envio — e reenviar não pode contar a
// mesma série duas vezes.
func (r *SessionRepo) AppendEvents(ctx context.Context, sessionID string, events []EventRow) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	q := r.tx.Q(ctx)
	inserted := 0

	for _, e := range events {
		payload := []byte("{}")
		if e.Payload != nil {
			b, err := json.Marshal(e.Payload)
			if err != nil {
				return inserted, fmt.Errorf("serializar evento %q: %w", e.Kind, err)
			}
			payload = b
		}
		tag, err := q.Exec(ctx,
			`INSERT INTO session_event (session_id, kind, step_index, payload, occurred_at)
			 VALUES ($1,$2,$3,$4,$5)
			 ON CONFLICT DO NOTHING`,
			sessionID, e.Kind, e.StepIndex, payload, e.OccurredAt)
		if err != nil {
			return inserted, fmt.Errorf("gravar evento %q: %w", e.Kind, err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// SessionFacts é o que os eventos dizem sobre a sessão, já agregado.
type SessionFacts struct {
	SetsCompleted int
	// DurationSeconds vem do `session_ended`. Zero enquanto não chegar — e uma
	// sessão sem fim declarado ainda está a decorrer.
	DurationSeconds int
	Ended           bool
	// Span é o tempo entre o primeiro e o último evento. Serve de travão de
	// sanidade: uma sessão que diz ter durado uma hora com todos os eventos
	// dentro de cinco segundos não durou uma hora.
	SpanSeconds int
}

// FactsFrom lê os eventos e diz o que aconteceu. **Não decide** — só conta.
func (r *SessionRepo) FactsFrom(ctx context.Context, sessionID string) (SessionFacts, error) {
	var f SessionFacts
	var first, last *time.Time

	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT kind, payload, occurred_at FROM session_event
		  WHERE session_id = $1 ORDER BY occurred_at`, sessionID)
	if err != nil {
		return f, fmt.Errorf("ler eventos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var kind string
		var payload map[string]any
		var at time.Time
		if err := rows.Scan(&kind, &payload, &at); err != nil {
			return f, err
		}
		if first == nil {
			t := at
			first = &t
		}
		t := at
		last = &t

		switch kind {
		case "set_completed":
			f.SetsCompleted++
		case "session_ended":
			f.Ended = true
			if v, ok := payload["durationSeconds"].(float64); ok {
				f.DurationSeconds = int(v)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return f, err
	}
	if first != nil && last != nil {
		f.SpanSeconds = int(last.Sub(*first).Seconds())
	}
	return f, nil
}

// UpdateOutcome fixa o veredicto na sessão.
func (r *SessionRepo) UpdateOutcome(ctx context.Context, sessionID, status string, durationSeconds, setsDone int) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_session
		    SET status = $2, duration_seconds = $3, sets_done = $4
		  WHERE id = $1`, sessionID, status, durationSeconds, setsDone)
	if err != nil {
		return fmt.Errorf("fixar veredicto: %w", err)
	}
	return nil
}

// OpenSession cria a sessão no estado `planned`, para os eventos terem onde
// aterrar. O veredicto chega com o `session_ended`.
func (r *SessionRepo) OpenSession(ctx context.Context, s SessionRow, prescriptions []PrescriptionRow) (string, error) {
	s.Status = "planned"
	return r.Insert(ctx, s, prescriptions)
}

// PlannedSeconds devolve o tempo que a sessão pedia.
func (r *SessionRepo) PlannedSeconds(ctx context.Context, sessionID, userID string) (int, error) {
	var planned int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT planned_seconds FROM workout_session WHERE id = $1 AND user_id = $2`,
		sessionID, userID).Scan(&planned)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return planned, err
}

// WithTx expõe o limite de transacção do repositório ao serviço, para que ele
// possa agrupar leitura e escrita sem conhecer o pool.
func (r *SessionRepo) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.tx.Do(ctx, fn)
}
