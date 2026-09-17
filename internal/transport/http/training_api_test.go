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
	// O contrato grava-se aqui, e não num teste à parte: ver `gravador_test.go`.
	gravarContrato(http.MethodGet, path, "", w)
	return w
}

// T4.7 — a sessão de hoje vem como pacote, auto-suficiente.
func TestTodayEndpoint(t *testing.T) {
	h, _, _ := serveTraining(t)
	w := get(t, h, "/v1/training/today?localDay=2026-09-12")

	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	// A resposta diz **qual** dos dois o dia é. Sem aula que sirva, é o plano.
	var envelope struct {
		Kind    string          `json:"kind"`
		Session json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Kind != "session" {
		t.Fatalf("sem aulas, o dia devia ser o plano; veio %q", envelope.Kind)
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
	if err := json.Unmarshal(envelope.Session, &pkg); err != nil {
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

/*
 * O treino vem como lista, e não só como linha do tempo.
 *
 * ⚠️ O pacote levava os **passos** — o que o Modo Foco percorre — e mais nada.
 * Os outros três ecrãs que mostram o treino precisam da lista de exercícios, e
 * por isso chamavam `buildSession` outra vez no telemóvel, com a mesma entrada.
 * A sessão já estava construída aqui e era deitada fora no handler.
 *
 * Enquanto assim foi, duas versões da app podiam propor treinos diferentes para
 * o mesmo dia à mesma conta — e nada no sistema dava por isso.
 */
func TestOTreinoVemComOPlanoDecidido(t *testing.T) {
	h, _, _ := serveTraining(t)
	w := get(t, h, "/v1/training/today?localDay=2026-09-12")
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Session struct {
			TotalSets int `json:"totalSets"`
			Plan      struct {
				Focus         string   `json:"focus"`
				FocusLabel    string   `json:"focusLabel"`
				Muscles       []string `json:"muscles"`
				BudgetMinutes int      `json:"budgetMinutes"`
				IsRecovery    bool     `json:"isRecovery"`
				MainSets      int      `json:"mainSets"`
				Exercises     []struct {
					ID           string `json:"id"`
					Name         string `json:"name"`
					Role         string `json:"role"`
					Sets         int    `json:"sets"`
					Target       int    `json:"target"`
					RestSeconds  int    `json:"restSeconds"`
					PatternLabel string `json:"patternLabel"`
				} `json:"exercises"`
			} `json:"plan"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	p := envelope.Session.Plan

	if len(p.Exercises) == 0 {
		t.Fatal("o plano veio sem exercícios — o ecrã não tem o que desenhar")
	}
	if p.BudgetMinutes <= 0 {
		t.Errorf("sem orçamento de minutos: %d", p.BudgetMinutes)
	}
	if p.FocusLabel == "" || p.Focus == "" {
		t.Errorf("foco sem rótulo: %q / %q", p.Focus, p.FocusLabel)
	}

	// As séries principais são a soma das séries do trabalho principal. É o
	// número que o ecrã do resumo mostrava, contado no telemóvel.
	soma := 0
	for _, e := range p.Exercises {
		if e.ID == "" || e.Name == "" || e.Role == "" {
			t.Errorf("exercício incompleto: %+v", e)
		}
		if e.Sets <= 0 || e.Target <= 0 {
			t.Errorf("%s sem séries ou alvo: %d × %d", e.ID, e.Sets, e.Target)
		}
		if e.PatternLabel == "" {
			t.Errorf("%s sem padrão em português", e.ID)
		}
		if e.Role == "main" {
			soma += e.Sets
		}
	}
	if p.MainSets != soma {
		t.Errorf("mainSets diz %d, a soma das séries principais é %d", p.MainSets, soma)
	}

	// Um dia de plano não é um dia de descanso — e se fosse, o ecrã diria outra
	// coisa em vez de propor exercícios.
	if p.IsRecovery && len(p.Exercises) > 0 {
		t.Error("dia de descanso com exercícios para fazer")
	}
}
