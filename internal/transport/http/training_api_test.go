package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// trainingProfiles devolve o que o motor precisa. Em produção lê o plano e o
// perfil; aqui é fixo, porque o que está a ser testado é o transporte.
type trainingProfiles struct{}

func (trainingProfiles) TrainingProfile(_ interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}, userID string, day time.Time) (service.TodayInput, error) {
	return service.TodayInput{
		PlanLabel: "Full Body", Experience: "intermediate",
		Equipment: []string{"dumbbells"}, WorkoutMinutes: 45, LocalDay: day,
	}, nil
}

func serveTraining(t *testing.T) (http.Handler, *pgxpool.Pool, string) {
	t.Helper()
	_, pool, userID := serve(t)

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	if _, err := catalog.SeedExercises(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog), training.DefaultConfig(),
		clock.NewFixed(time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)))

	router := airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Training:    &handlers.Training{Service: svc, Profiles: trainingProfiles{}},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
	return router, pool, userID
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// T4.7 — a sessão de hoje vem como pacote, auto-suficiente.
func TestTodayEndpoint(t *testing.T) {
	h, _, _ := serveTraining(t)
	w := get(t, h, "/v1/training/today?localDay=2026-09-12")

	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var pkg struct {
		SessionID string `json:"sessionId"`
		TotalSets int    `json:"totalSets"`
		Steps     []struct {
			Index int `json:"index"`
			Dial  struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"dial"`
			Action struct {
				Label string `json:"label"`
				Icon  string `json:"icon"`
			} `json:"action"`
			Controls struct {
				RemoveSet struct {
					Enabled bool   `json:"enabled"`
					Reason  string `json:"reason"`
				} `json:"removeSet"`
			} `json:"controls"`
		} `json:"steps"`
		CompletionThresholdSeconds int `json:"completionThresholdSeconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.SessionID == "" || pkg.TotalSets == 0 || len(pkg.Steps) == 0 {
		t.Fatalf("pacote incompleto: %+v", pkg)
	}
	// Cada passo traz tudo o que a interface mostra.
	for i, step := range pkg.Steps {
		if step.Index != i || step.Dial.Label == "" || step.Action.Label == "" || step.Action.Icon == "" {
			t.Fatalf("passo %d incompleto: %+v", i, step)
		}
	}
	// E o primeiro passo tem o controlo já resolvido, com razão.
	first := pkg.Steps[0].Controls.RemoveSet
	if !first.Enabled && first.Reason == "" {
		t.Error("controlo desligado sem razão — a interface não consegue explicar")
	}
	t.Logf("%d passos · %d séries · limiar %ds", len(pkg.Steps), pkg.TotalSets, pkg.CompletionThresholdSeconds)
}

// ⚠️ O pacote não leva nada com que o cliente possa decidir.
func TestTodayPackageCarriesNoRuleInputs(t *testing.T) {
	h, _, _ := serveTraining(t)
	body := get(t, h, "/v1/training/today?localDay=2026-09-12").Body.String()
	for _, forbidden := range []string{"\"score\"", "\"setsAhead\"", "\"scores\""} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("o pacote leva %s", forbidden)
		}
	}
}

func recordBody(planned, duration int) string {
	return fmt.Sprintf(`{"occurredAt":"2026-09-12T18:32:00Z","localDay":"2026-09-12",
	 "plannedSeconds":%d,"durationSeconds":%d,"setsDone":12}`, planned, duration)
}

// **T4.9 — o servidor decide, e o corpo não traz `status`.**
func TestRecordEndpointDecidesStatus(t *testing.T) {
	h, _, _ := serveTraining(t)

	skipped := post(t, h, "/v1/training/sessions", recordBody(2700, 5), nil)
	if skipped.Code != http.StatusCreated {
		t.Fatalf("%d: %s", skipped.Code, skipped.Body.String())
	}
	var s struct {
		Status          string `json:"status"`
		CountsForStreak bool   `json:"countsForStreak"`
		Streak          int    `json:"streak"`
	}
	if err := json.Unmarshal(skipped.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Status != "skipped" || s.CountsForStreak {
		t.Fatalf("5 segundos: %+v", s)
	}

	done := post(t, h, "/v1/training/sessions", recordBody(2700, 2700), nil)
	var d struct {
		Status          string `json:"status"`
		CountsForStreak bool   `json:"countsForStreak"`
		Streak          int    `json:"streak"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "completed" || !d.CountsForStreak || d.Streak < 1 {
		t.Fatalf("sessão inteira: %+v", d)
	}
	t.Logf("5 s → %s · 2700 s → %s (sequência %d)", s.Status, d.Status, d.Streak)
}

// Mandar `status` no corpo é recusado: é a porta das traseiras da regra.
func TestStatusInBodyIsRefused(t *testing.T) {
	h, _, _ := serveTraining(t)
	body := `{"occurredAt":"2026-09-12T18:32:00Z","localDay":"2026-09-12",
	 "plannedSeconds":2700,"durationSeconds":5,"setsDone":12,"status":"completed"}`
	w := post(t, h, "/v1/training/sessions", body, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d: um `status` no corpo devia ser recusado — %s", w.Code, w.Body.String())
	}
}

// Reenviar devolve o mesmo registo, e não cria um segundo treino.
func TestRecordIsIdempotentOverHTTP(t *testing.T) {
	h, pool, _ := serveTraining(t)
	key := map[string]string{"Idempotency-Key": "44444444-4444-4444-8444-444444444444"}

	first := post(t, h, "/v1/training/sessions", recordBody(2700, 2700), key)
	if first.Code != http.StatusCreated {
		t.Fatal(first.Body.String())
	}
	second := post(t, h, "/v1/training/sessions", recordBody(2700, 2700), key)
	if second.Body.String() != first.Body.String() {
		t.Fatalf("reenvio diferente:\n %s\n %s", first.Body.String(), second.Body.String())
	}

	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workout_session`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d sessões — o reenvio criou outra", n)
	}
}

// O dia do utilizador é obrigatório e explícito.
func TestLocalDayIsValidated(t *testing.T) {
	h, _, _ := serveTraining(t)
	body := `{"occurredAt":"2026-09-12T18:32:00Z","localDay":"12/09/2026",
	 "plannedSeconds":2700,"durationSeconds":2700,"setsDone":12}`
	w := post(t, h, "/v1/training/sessions", body, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "localDay") {
		t.Errorf("o erro devia apontar o campo: %s", w.Body.String())
	}
}

// Sem token não se entra, nem para ler o treino de hoje.
func TestTrainingNeedsAuth(t *testing.T) {
	h, _, _ := serveTraining(t)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/training/today", nil),
		httptest.NewRequest(http.MethodPost, "/v1/training/sessions", bytes.NewBufferString(recordBody(2700, 2700))),
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s deu %d", req.Method, req.URL.Path, w.Code)
		}
	}
}
