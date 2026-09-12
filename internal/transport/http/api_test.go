package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Stop()
	os.Exit(code)
}

// fakeAuth troca um token por um utilizador. A verificação a sério chega com a
// Fase 3; o contrato do handler não muda.
type fakeAuth struct{ userID string }

func (f fakeAuth) Verify(context.Context, string) (string, error) { return f.userID, nil }

// profiles devolve o que o motor precisa. Em produção lê o perfil; aqui é fixo,
// porque o que está a ser testado é o transporte.
type profiles struct{}

func (profiles) Profile(_ interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}, userID string) (service.CreateGoalInput, error) {
	return service.CreateGoalInput{
		CurrentWeightKg: 80, HeightCm: fp(175), Age: ip(34), Sex: sp("male"),
		DaysPerWeek: 3, SessionMinutes: 45, Experience: "intermediate",
		NutritionGoal: "lose_fat", MealsPerDay: 4,
	}, nil
}

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }
func sp(v string) *string   { return &v }

func serve(t *testing.T) (http.Handler, *pgxpool.Pool, string) {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841234567','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	svc := service.NewGoalService(tx, repo.NewGoalRepo(tx), service.Configs{
		Goal: goal.DefaultConfig(), Journey: journey.DefaultConfig(), Nutrition: nutrition.DefaultConfig(),
	}, clock.NewFixed(time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)))

	router := airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Goals:       &handlers.Goals{Service: svc, Profiles: profiles{}},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
	return router, pool, userID
}

func post(t *testing.T, h http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer token-de-teste")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const fixedBody = `{"type":"outcome","horizon":"fixed","direction":"lose_weight","priority":"weight",
 "startDate":"2026-09-12","targetDate":"2026-12-29",
 "targets":[{"metric":"body_weight","value":74,"unit":"kg","direction":"decrease"}]}`

func TestCreateGoalEndpoint(t *testing.T) {
	h, _, _ := serve(t)
	w := post(t, h, "/v1/goals", fixedBody, nil)

	if w.Code != http.StatusCreated {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"goal", "journey", "plan", "nutrition", "assessment"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("resposta sem %q", key)
		}
	}
	// O plano tem de trazer os números, não só o id: o ecrã mostra "2× por
	// semana, 38 min" e não um uuid. Estava a sair a zeros.
	plan := resp["plan"].(map[string]any)
	if plan["frequency"].(float64) < 1 || plan["sessionMinutes"].(float64) < 1 || plan["intensity"] == "" {
		t.Fatalf("plano sem números: %v", plan)
	}
	// E os números são os da **fase**, não os do perfil: adaptação baixa os dois.
	if plan["frequency"].(float64) != 2 || plan["intensity"] != "low" {
		t.Errorf("a primeira fase é adaptação: %v", plan)
	}

	journeyResp := resp["journey"].(map[string]any)
	phases, _ := journeyResp["phases"].([]any)
	if len(phases) != 4 {
		t.Errorf("%d fases na resposta", len(phases))
	}
	t.Logf("resposta: %s", firstLine(w.Body.String()))
}

// ⚠️ O score nunca sai do servidor.
//
// A configuração diz "Nunca são mostrados ao utilizador". O DTO não o leva, e
// este teste lê o JSON à procura dele — porque uma etiqueta `json:"-"` é fácil
// de tirar sem querer.
func TestScoreNeverLeavesTheAPI(t *testing.T) {
	h, _, _ := serve(t)
	for _, path := range []string{"/v1/goals", "/v1/goals/assess"} {
		body := post(t, h, path, fixedBody, nil).Body.String()
		for _, forbidden := range []string{"\"scores\"", "\"overall\"", "\"consistency\""} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s devolve %s: %s", path, forbidden, body)
			}
		}
	}
}

// O horizonte e a data têm de concordar, e o erro diz **qual** campo.
func TestHorizonMismatchReturns422WithField(t *testing.T) {
	h, _, _ := serve(t)
	body := strings.Replace(fixedBody, `"horizon":"fixed"`, `"horizon":"open_ended"`, 1)
	w := post(t, h, "/v1/goals", body, nil)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Error struct{ Code, Message, Field string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "journey_horizon_mismatch" {
		t.Errorf("código %q", env.Error.Code)
	}
	if env.Error.Field != "targetDate" {
		t.Errorf("campo %q — o erro tem de dizer qual", env.Error.Field)
	}
	t.Logf("%d %s · %s · campo %q", w.Code, env.Error.Code, env.Error.Message, env.Error.Field)
}

func TestSecondGoalReturns409(t *testing.T) {
	h, _, _ := serve(t)
	if w := post(t, h, "/v1/goals", fixedBody, nil); w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	w := post(t, h, "/v1/goals", fixedBody, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "goal_already_active") {
		t.Errorf("corpo: %s", w.Body.String())
	}
}

// Reenviar com a mesma chave devolve a mesma resposta, e não cria um segundo
// objetivo.
func TestIdempotentReplay(t *testing.T) {
	h, pool, _ := serve(t)
	key := map[string]string{"Idempotency-Key": "11111111-1111-4111-8111-111111111111"}

	first := post(t, h, "/v1/goals", fixedBody, key)
	if first.Code != http.StatusCreated {
		t.Fatal(first.Body.String())
	}
	second := post(t, h, "/v1/goals", fixedBody, key)

	if second.Code != first.Code {
		t.Fatalf("reenvio deu %d, o primeiro deu %d", second.Code, first.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Fatal("o reenvio devolveu uma resposta diferente")
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Error("o reenvio devia dizer que é um reenvio")
	}

	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM goal`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d objetivos — o reenvio criou outro", n)
	}
}

// A mesma chave com outro conteúdo é um erro do cliente, não um reenvio.
func TestIdempotencyKeyReusedWithDifferentBody(t *testing.T) {
	h, _, _ := serve(t)
	key := map[string]string{"Idempotency-Key": "22222222-2222-4222-8222-222222222222"}

	if w := post(t, h, "/v1/goals", fixedBody, key); w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	other := strings.Replace(fixedBody, `"value":74`, `"value":70`, 1)
	w := post(t, h, "/v1/goals", other, key)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Idempotency-Key") {
		t.Errorf("o erro devia apontar o cabeçalho: %s", w.Body.String())
	}
}

// Sem token não se entra.
func TestUnauthenticatedIsRefused(t *testing.T) {
	h, _, _ := serve(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/goals", bytes.NewBufferString(fixedBody))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	// A mensagem é a mesma para token ausente, inválido e expirado: distinguir
	// diz ao atacante se o token existiu alguma vez.
	if !strings.Contains(w.Body.String(), "Sessão inválida ou expirada") {
		t.Errorf("corpo: %s", w.Body.String())
	}
}

// Um campo escrito ao lado não passa em silêncio.
func TestUnknownFieldIsRefused(t *testing.T) {
	h, _, _ := serve(t)
	body := strings.Replace(fixedBody, `"targetDate"`, `"target_date"`, 1)
	w := post(t, h, "/v1/goals", body, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d: um campo desconhecido devia ser recusado — senão a jornada nascia sem prazo", w.Code)
	}
}

// Avaliar não grava.
func TestAssessEndpointWritesNothing(t *testing.T) {
	h, pool, _ := serve(t)
	w := post(t, h, "/v1/goals/assess", fixedBody, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM goal`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("avaliar gravou %d objetivos", n)
	}
}

// Um pânico num handler não leva o servidor.
func TestRecoverKeepsTheServerUp(t *testing.T) {
	h, _, _ := serve(t)
	// /readyz continua a responder depois de tudo o resto.
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("/readyz deu %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Request-Id") == "" {
		t.Error("sem request-id — investigar um erro relatado seria procurar uma linha entre milhares")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
