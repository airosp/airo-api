package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openSession(t *testing.T, svc *service.TrainingService, userID string) (string, int) {
	t.Helper()
	id, err := svc.Open(context.Background(), userID, todayInput())
	if err != nil {
		t.Fatal(err)
	}
	_, _, steps, _ := svc.Today(todayInput())
	planned := 0
	for range steps {
	}
	// O tempo pedido vem da própria sessão gravada.
	return id, planned
}

func at(minutes int) time.Time {
	return time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
}

func ev(kind string, index int, minutes int, payload map[string]any) service.SessionEvent {
	i := index
	return service.SessionEvent{Kind: kind, StepIndex: &i, At: at(minutes), Payload: payload}
}

func send(t *testing.T, svc *service.TrainingService, userID, sessionID string, events ...service.SessionEvent) service.AppendEventsResult {
	t.Helper()
	out, err := svc.AppendEvents(context.Background(), userID, sessionID, events,
		time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sessionStatus(t *testing.T, pool *pgxpool.Pool, id string) (string, int, int) {
	t.Helper()
	var status string
	var duration, setsDone int
	if err := pool.QueryRow(context.Background(),
		`SELECT status, duration_seconds, sets_done FROM workout_session WHERE id = $1`, id,
	).Scan(&status, &duration, &setsDone); err != nil {
		t.Fatal(err)
	}
	return status, duration, setsDone
}

// T4.10 — o lote entra, e o servidor decide no `session_ended`.
func TestEventsDecideAtTheEnd(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	id, _ := openSession(t, svc, userID)

	// Antes do fim: a sessão continua a decorrer, sem veredicto.
	mid := send(t, svc, userID, id,
		ev("step_advanced", 0, 0, nil),
		ev("set_completed", 1, 1, map[string]any{"reps": 12}),
		ev("step_advanced", 2, 2, nil),
	)
	if mid.Ended || mid.Status != "" {
		t.Fatalf("ainda não acabou: %+v", mid)
	}
	if status, _, _ := sessionStatus(t, pool, id); status != "planned" {
		t.Fatalf("estado %q antes do fim", status)
	}

	// O fim traz a duração, **não** o estado.
	end := send(t, svc, userID, id,
		service.SessionEvent{Kind: "session_ended", At: at(46),
			Payload: map[string]any{"durationSeconds": 2760}})
	if !end.Ended {
		t.Fatal("devia ter terminado")
	}
	if end.Status != "completed" || !end.CountsForStreak {
		t.Fatalf("46 minutos: %+v", end)
	}
	status, duration, sets := sessionStatus(t, pool, id)
	if status != "completed" || duration == 0 || sets != 1 {
		t.Fatalf("gravado: %s, %ds, %d séries", status, duration, sets)
	}
	t.Logf("%d eventos aceites → %s, %ds, %d séries", end.Accepted, status, duration, sets)
}

// Fora de ordem é normal: o cliente acumula e envia quando pode.
func TestEventsArriveOutOfOrder(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	id, _ := openSession(t, svc, userID)

	// O fim chega **primeiro**, e as séries depois.
	send(t, svc, userID, id,
		service.SessionEvent{Kind: "session_ended", At: at(50),
			Payload: map[string]any{"durationSeconds": 3000}})
	send(t, svc, userID, id,
		ev("set_completed", 1, 2, nil),
		ev("set_completed", 3, 5, nil),
		ev("set_completed", 5, 9, nil))

	_, _, sets := sessionStatus(t, pool, id)
	if sets != 3 {
		t.Fatalf("%d séries — as que chegaram depois do fim têm de contar", sets)
	}
}

// Reenviar o mesmo lote não conta nada duas vezes.
func TestEventsAreIdempotent(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	id, _ := openSession(t, svc, userID)

	batch := []service.SessionEvent{
		ev("set_completed", 1, 1, nil),
		ev("set_completed", 3, 4, nil),
		ev("set_completed", 5, 8, nil),
	}
	first := send(t, svc, userID, id, batch...)
	second := send(t, svc, userID, id, batch...)

	if first.Accepted != 3 {
		t.Fatalf("primeiro lote: %d aceites", first.Accepted)
	}
	if second.Accepted != 0 || second.Duplicates != 3 {
		t.Fatalf("reenvio: %d aceites, %d repetidos", second.Accepted, second.Duplicates)
	}

	send(t, svc, userID, id,
		service.SessionEvent{Kind: "session_ended", At: at(50), Payload: map[string]any{"durationSeconds": 3000}})
	if _, _, sets := sessionStatus(t, pool, id); sets != 3 {
		t.Fatalf("%d séries — o reenvio contou a dobrar", sets)
	}
}

// **Cinco segundos não são quarenta e cinco minutos.**
//
// Um cliente que declarasse uma hora com todos os eventos dentro de cinco
// segundos não treinou uma hora. O tempo que os eventos abrangem é o travão.
func TestDeclaredDurationIsCappedByTheEventSpan(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	id, _ := openSession(t, svc, userID)

	// Todos os eventos no mesmo instante, mas a declarar 50 minutos.
	send(t, svc, userID, id,
		service.SessionEvent{Kind: "step_advanced", At: at(0)},
		service.SessionEvent{Kind: "session_ended", At: at(0).Add(3 * time.Second),
			Payload: map[string]any{"durationSeconds": 3000}})

	status, duration, _ := sessionStatus(t, pool, id)
	if status != "skipped" {
		t.Fatalf("declarou 3000 s em 3 s reais e saiu %q", status)
	}
	if duration > 10 {
		t.Fatalf("duração gravada %ds — devia ser a real", duration)
	}
	t.Logf("declarou 3000 s, os eventos abrangem 3 s → %s, %ds", status, duration)
}

// Um evento sem instante não se pode ordenar nem deduplicar. Recusa-se o
// evento, não o lote: perder trinta séries por causa de uma é pior.
func TestEventWithoutTimestampIsDroppedNotTheBatch(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	id, _ := openSession(t, svc, userID)

	out := send(t, svc, userID, id,
		ev("set_completed", 1, 1, nil),
		service.SessionEvent{Kind: "set_completed"}, // sem instante
		ev("set_completed", 3, 4, nil),
	)
	if out.Accepted != 2 {
		t.Fatalf("%d aceites — os dois válidos tinham de entrar", out.Accepted)
	}
	send(t, svc, userID, id,
		service.SessionEvent{Kind: "session_ended", At: at(50), Payload: map[string]any{"durationSeconds": 3000}})
	if _, _, sets := sessionStatus(t, pool, id); sets != 2 {
		t.Fatalf("%d séries", sets)
	}
}

// A sessão de outro utilizador não se toca.
func TestEventsOfAnotherUserAreRefused(t *testing.T) {
	svc, pool, userID := trainingSetup(t)
	id, _ := openSession(t, svc, userID)

	var other string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258849999999','MZ') RETURNING id`,
	).Scan(&other); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AppendEvents(context.Background(), other, id,
		[]service.SessionEvent{ev("set_completed", 1, 1, nil)},
		time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("devia ser recusado")
	}
}
