package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/migrations"
)

func serveHistorico(t *testing.T) http.Handler {
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
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258847776665','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO profile (user_id, display_name, age_years, sex, height_cm,
		                      experience, workout_days, workout_minutes, profile_complete)
		 VALUES ($1,'Sandra',30,'female',168,'beginner','{0,2,4}',35,true)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO measurement (user_id, metric, value, unit, recorded_at)
		 VALUES ($1,'body_weight',72,'kg',now())`, userID); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	// A sessão é remontada a partir do catálogo, e sem ele o servidor não sabe
	// que treino foi — que é precisamente o que o impede de aceitar um que a
	// Airo nunca propôs.
	if _, err := catalog.SeedExercises(ctx); err != nil {
		t.Fatal(err)
	}
	sessions := repo.NewSessionRepo(tx, catalog)
	fixed := clock.NewFixed(time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC))

	return airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test", DB: pool,
		Auth: fakeAuth{userID: userID},
		Training: &handlers.Training{
			Service:  service.NewTrainingService(sessions, training.DefaultConfig(), fixed),
			Profiles: service.NewProfiles(repo.NewProfileRepo(tx), nil, repo.NewPreferenceRepo(tx)),
			Sessions: sessions,
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

func gravarTreino(t *testing.T, h http.Handler, chave, dia string, duracao int) *httptest.ResponseRecorder {
	t.Helper()
	corpo := fmt.Sprintf(`{
	  "occurredAt":"%sT18:40:00Z","localDay":"%s",
	  "plannedSeconds":2100,"durationSeconds":%d,"setsDone":9,
	  "blocks":{"warmupSeconds":300,"mainSeconds":%d,"cooldownSeconds":240}
	}`, dia, dia, duracao, duracao-540)
	r := httptest.NewRequest(http.MethodPost, "/v1/training/sessions", strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer token-de-teste")
	r.Header.Set("Idempotency-Key", chave)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func historico(t *testing.T, h http.Handler, de, ate string) dto.SessionHistoryResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/training/sessions?from="+de+"&to="+ate, nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	var out dto.SessionHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// O histórico sobrevive: grava-se um treino e lê-se de volta.
func TestHistoricoGravaELeDeVolta(t *testing.T) {
	h := serveHistorico(t)

	if w := gravarTreino(t, h, "treino_1789279", "2026-09-13", 1800); w.Code != http.StatusCreated {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}

	out := historico(t, h, "2026-09-13", "2026-09-13")
	if len(out.Sessions) != 1 {
		t.Fatalf("%d sessões", len(out.Sessions))
	}
	s := out.Sessions[0]
	// O identificador que volta é o do telemóvel: é por ele que o aparelho
	// reconhece o que já gravou, em vez de duplicar o histórico ao reentrar.
	if s.ID != "treino_1789279" {
		t.Fatalf("id = %q, esperava a chave do telemóvel", s.ID)
	}
	if s.DurationSeconds != 1800 || s.PlannedSeconds != 2100 || s.SetsDone != 9 {
		t.Fatalf("sessão = %+v", s)
	}
	// Metade do que a sessão pedia é o limiar. 1800 de 2100 conta.
	if s.Status != "completed" {
		t.Fatalf("estado = %q", s.Status)
	}
	if s.Blocks == nil || s.Blocks.WarmupSeconds != 300 || s.Blocks.CooldownSeconds != 240 {
		t.Fatalf("blocos = %+v", s.Blocks)
	}
	if s.Title == "" || s.Focus == "" {
		t.Fatal("o título e o foco vêm do servidor, que remonta a sessão")
	}
}

// Reenviar não duplica: é a chave de idempotência a fazer o seu trabalho.
func TestReenviarTreinoNaoDuplica(t *testing.T) {
	h := serveHistorico(t)

	primeiro := gravarTreino(t, h, "treino_mesmo", "2026-09-13", 1800)
	if primeiro.Code != http.StatusCreated {
		t.Fatalf("primeiro = %d: %s", primeiro.Code, primeiro.Body.String())
	}
	// O código da repetição depende de **quem** a apanha, e os dois são
	// legítimos: com a cache quente é o middleware a repetir a resposta
	// original (201, com `Idempotent-Replay`); depois de ela expirar é o
	// repositório a reconhecer a chave (200). O que não pode mudar é o número
	// de treinos.
	for i := 0; i < 3; i++ {
		w := gravarTreino(t, h, "treino_mesmo", "2026-09-13", 1800)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("repetição %d = %d: %s", i, w.Code, w.Body.String())
		}
		if w.Code == http.StatusCreated && w.Header().Get("Idempotent-Replay") != "true" {
			t.Fatalf("repetição %d devolveu 201 sem dizer que era repetição", i)
		}
	}
	if out := historico(t, h, "2026-09-13", "2026-09-13"); len(out.Sessions) != 1 {
		t.Fatalf("%d sessões depois de quatro envios", len(out.Sessions))
	}
}

// Uma sessão curta grava na mesma, e o servidor é que lhe chama saltada.
//
// **Uma sessão saltada grava sempre** — invariante 7. A adesão precisa de
// distinguir quem abriu e desistiu de quem nunca apareceu.
func TestTreinoCurtoContaComoSaltado(t *testing.T) {
	h := serveHistorico(t)

	if w := gravarTreino(t, h, "treino_curto", "2026-09-12", 120); w.Code != http.StatusCreated {
		t.Fatalf("= %d: %s", w.Code, w.Body.String())
	}
	out := historico(t, h, "2026-09-12", "2026-09-12")
	if len(out.Sessions) != 1 {
		t.Fatalf("uma sessão de dois minutos tem de ficar registada; ficaram %d", len(out.Sessions))
	}
	if out.Sessions[0].Status != "skipped" {
		t.Fatalf("estado = %q, dois minutos de 35 não é um treino", out.Sessions[0].Status)
	}
}

func TestHistoricoPorIntervalo(t *testing.T) {
	h := serveHistorico(t)
	for i, dia := range []string{"2026-09-09", "2026-09-11", "2026-09-13"} {
		if w := gravarTreino(t, h, fmt.Sprintf("t_%d", i), dia, 1800); w.Code != http.StatusCreated {
			t.Fatal(w.Body.String())
		}
	}
	if out := historico(t, h, "2026-09-10", "2026-09-13"); len(out.Sessions) != 2 {
		t.Fatalf("%d sessões no intervalo", len(out.Sessions))
	}
	// Da mais recente para a mais antiga: é a ordem em que o histórico se lê.
	out := historico(t, h, "2026-09-09", "2026-09-13")
	if len(out.Sessions) != 3 || out.Sessions[0].LocalDay != "2026-09-13" {
		t.Fatalf("ordem = %v", []string{out.Sessions[0].LocalDay})
	}
}

func TestHistoricoIntervaloInvalido(t *testing.T) {
	h := serveHistorico(t)
	casos := [][2]string{{"ontem", "2026-09-13"}, {"2026-09-13", "2026-09-01"}, {"2020-01-01", "2026-12-31"}}
	for _, c := range casos {
		r := httptest.NewRequest(http.MethodGet, "/v1/training/sessions?from="+c[0]+"&to="+c[1], nil)
		r.Header.Set("Authorization", "Bearer token-de-teste")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%v = %d, esperava 422", c, w.Code)
		}
	}
}
