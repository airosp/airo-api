package http_test

import (
	"context"
	"encoding/json"
	"net/http"
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

// Um treino sem carga grava como sempre gravou.
func TestTreinoSemCargaContinuaAGravar(t *testing.T) {
	h, pool, userID := serveTraining(t)

	corpo := `{"occurredAt":"2026-09-12T19:00:00Z","localDay":"2026-09-12",
	           "plannedSeconds":2700,"durationSeconds":2600,"setsDone":8}`
	if w := postComChave(t, h, "/v1/training/sessions", corpo, "sem-carga-1"); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	var comActual int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM exercise_set s
		   JOIN exercise_prescription p ON p.id = s.prescription_id
		   JOIN workout_session ws ON ws.id = p.session_id
		  WHERE ws.user_id = $1 AND s.actual IS NOT NULL`, userID).Scan(&comActual); err != nil {
		t.Fatal(err)
	}
	if comActual != 0 {
		t.Errorf("%d séries com `actual` num treino que não mandou nenhum", comActual)
	}
}

/*
 * O que se fez volta no histórico, já escrito para se ler.
 *
 * ⚠️ As prescrições e as séries eram escritas desde o princípio e **nunca
 * consultadas**: o histórico sabia que tinham sido feitas oito séries e não
 * sabia dizer de quê, nem com quantos quilos. Escrever sem ler é pagar o custo
 * e não receber o valor.
 */
func TestOQueSeFezVoltaNoHistorico(t *testing.T) {
	h, pool, userID := serveComHistorico(t)
	ctx := context.Background()

	// Primeiro um treino simples, só para saber que exercícios o motor montou.
	simples := `{"occurredAt":"2026-09-12T18:00:00Z","localDay":"2026-09-12",
	             "plannedSeconds":2700,"durationSeconds":2600,"setsDone":8}`
	if w := postComChave(t, h, "/v1/training/sessions", simples, "hist-1"); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("primeiro treino: %d — %s", w.Code, w.Body.String())
	}

	var slug string
	if err := pool.QueryRow(ctx,
		`SELECT e.slug
		   FROM exercise_prescription p
		   JOIN workout_session ws ON ws.id = p.session_id
		   JOIN exercise e ON e.id = p.exercise_id
		  WHERE ws.user_id = $1
		  ORDER BY p.position LIMIT 1`, userID).Scan(&slug); err != nil {
		t.Fatalf("descobrir o exercício: %v", err)
	}

	// Agora um com carga, no mesmo dia.
	comCarga := `{"occurredAt":"2026-09-12T19:30:00Z","localDay":"2026-09-12",
	              "plannedSeconds":2700,"durationSeconds":2600,"setsDone":8,
	              "performed":[{"exerciseId":"` + slug + `","sets":[
	                {"index":0,"reps":10,"weightKg":40,"completed":true},
	                {"index":1,"reps":8,"weightKg":42.5,"completed":true}
	              ]}]}`
	if w := postComChave(t, h, "/v1/training/sessions", comCarga, "hist-2"); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("treino com carga: %d — %s", w.Code, w.Body.String())
	}

	w := get(t, h, "/v1/training/sessions?from=2026-09-01&to=2026-09-30")
	if w.Code != http.StatusOK {
		t.Fatalf("histórico: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Sessions []struct {
			ID        string `json:"id"`
			Performed []struct {
				ExerciseID string   `json:"exerciseId"`
				Name       string   `json:"name"`
				Target     string   `json:"target"`
				Done       []string `json:"done"`
			} `json:"performed"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	var comDetalhe, semDetalhe int
	var frase string
	for _, s := range body.Sessions {
		if len(s.Performed) == 0 {
			semDetalhe++
			continue
		}
		comDetalhe++
		for _, e := range s.Performed {
			if e.ExerciseID == slug && len(e.Done) > 0 {
				frase = e.Done[0]
			}
			if e.Name == "" {
				t.Error("exercício sem nome: o ecrã não tem o que escrever")
			}
		}
	}
	if comDetalhe != 1 {
		t.Errorf("%d sessões com detalhe, esperava 1", comDetalhe)
	}
	if semDetalhe != 1 {
		t.Errorf("%d sessões sem detalhe, esperava 1 — a que não mandou carga", semDetalhe)
	}
	if frase != "10 × 40 kg" {
		t.Errorf("a frase veio %q, esperava \"10 × 40 kg\"", frase)
	}
}

// serveComHistorico é o `serveTraining` com o leitor do histórico ligado — sem
// ele o `GET /v1/training/sessions` responde que está indisponível.
func serveComHistorico(t *testing.T) (http.Handler, *pgxpool.Pool, string) {
	t.Helper()
	_, pool, userID := serve(t)

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	if _, err := catalog.SeedExercises(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions := repo.NewSessionRepo(tx, catalog)
	fixed := clock.NewFixed(time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC))

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth: fakeAuth{userID: userID},
		Training: &handlers.Training{
			Service:  service.NewTrainingService(sessions, training.DefaultConfig(), fixed),
			Profiles: trainingProfiles{},
			Sessions: sessions,
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), pool, userID
}

/*
 * A carga de hoje é proposta a partir da de ontem.
 *
 * Sem histórico não havia de onde propor — e era essa a metade que faltava à
 * progressão: a app sabia quanto se levantou e não dizia quanto levantar.
 */
func TestACargaDeHojeSaiDaDeOntem(t *testing.T) {
	h, pool, userID := serveComHistorico(t)
	ctx := context.Background()

	simples := `{"occurredAt":"2026-09-12T18:00:00Z","localDay":"2026-09-12",
	             "plannedSeconds":2700,"durationSeconds":2600,"setsDone":8}`
	if w := postComChave(t, h, "/v1/training/sessions", simples, "prog-1"); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("primeiro treino: %d", w.Code)
	}

	var slug string
	var series int
	if err := pool.QueryRow(ctx,
		`SELECT e.slug, p.sets
		   FROM exercise_prescription p
		   JOIN workout_session ws ON ws.id = p.session_id
		   JOIN exercise e ON e.id = p.exercise_id
		  WHERE ws.user_id = $1 ORDER BY p.position LIMIT 1`, userID).Scan(&slug, &series); err != nil {
		t.Fatal(err)
	}

	// Um treino com todas as séries feitas, a 40 kg.
	feitas := make([]string, 0, series)
	for i := 0; i < series; i++ {
		feitas = append(feitas, `{"index":`+itoa(i)+`,"reps":10,"weightKg":40,"completed":true}`)
	}
	corpo := `{"occurredAt":"2026-09-12T19:30:00Z","localDay":"2026-09-12",
	           "plannedSeconds":2700,"durationSeconds":2600,"setsDone":8,
	           "performed":[{"exerciseId":"` + slug + `","sets":[` + join(feitas) + `]}]}`
	if w := postComChave(t, h, "/v1/training/sessions", corpo, "prog-2"); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("treino com carga: %d — %s", w.Code, w.Body.String())
	}

	w := get(t, h, "/v1/training/load-suggestions")
	if w.Code != http.StatusOK {
		t.Fatalf("propostas: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Suggestions []struct {
			ExerciseID string  `json:"exerciseId"`
			LastKg     float64 `json:"lastKg"`
			SuggestKg  float64 `json:"suggestKg"`
			Reason     string  `json:"reason"`
		} `json:"suggestions"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)

	var achou bool
	for _, p := range body.Suggestions {
		if p.ExerciseID != slug {
			continue
		}
		achou = true
		if p.LastKg != 40 {
			t.Errorf("última carga %v, esperava 40", p.LastKg)
		}
		if p.SuggestKg != 42.5 {
			t.Errorf("propôs %v, esperava 42.5 — um degrau acima", p.SuggestKg)
		}
		if p.Reason == "" {
			t.Error("proposta sem motivo: o ecrã mostra um número sem explicação")
		}
	}
	if !achou {
		t.Fatalf("nenhuma proposta para %q: %+v", slug, body.Suggestions)
	}
}

// Sem carga registada não há proposta nenhuma — não se inventa um número.
func TestSemHistoricoNaoHaProposta(t *testing.T) {
	h, _, _ := serveComHistorico(t)
	w := get(t, h, "/v1/training/load-suggestions")
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	var body struct {
		Suggestions []any `json:"suggestions"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Suggestions) != 0 {
		t.Errorf("propôs %d cargas sem nunca ter visto ninguém levantar nada", len(body.Suggestions))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func join(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
