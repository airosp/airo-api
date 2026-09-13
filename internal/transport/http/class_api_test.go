package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// ⚠️ Numa aula, o **tempo planeado é o da aula** — não o que o cliente diz.
//
// Deixá-lo mandar o tempo planeado era deixá-lo decidir se o treino contou: com
// `plannedSeconds: 5`, quarenta segundos de vídeo passavam por sessão completa.
// É a mesma porta das traseiras que o `status` no corpo seria.
func TestNumaAulaOTempoPlaneadoEODaAula(t *testing.T) {
	h, _ := serveTrainingComAulas(t, 1500) // aula de 25 minutos

	// O cliente mente: diz que a aula pedia 5 segundos.
	corpo := `{"classId":"aula_x","occurredAt":"2026-09-13T09:00:00Z","localDay":"2026-09-13",
	           "plannedSeconds":5,"durationSeconds":40,"setsDone":0}`
	w := postComChave(t, h, "/v1/training/sessions", corpo, "mentira-1")
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	var got struct {
		Status          string `json:"status"`
		CountsForStreak bool   `json:"countsForStreak"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)

	// 40 segundos de uma aula de 25 minutos é desistir, digam o que disserem.
	if got.Status != "skipped" {
		t.Errorf("40 s de uma aula de 25 min deu %q — o cliente conseguiu decidir", got.Status)
	}
	if got.CountsForStreak {
		t.Error("contou para a sequência")
	}
}

// E o caminho certo: quase toda a aula conta.
func TestAulaQuaseTodaConta(t *testing.T) {
	h, _ := serveTrainingComAulas(t, 1500)
	corpo := `{"classId":"aula_x","occurredAt":"2026-09-13T09:00:00Z","localDay":"2026-09-13",
	           "plannedSeconds":99999,"durationSeconds":1380,"setsDone":0}`
	w := postComChave(t, h, "/v1/training/sessions", corpo, "verdade-1")

	var got struct {
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Status != "completed" {
		t.Errorf("23 min de 25 deu %q", got.Status)
	}
}

// Uma aula que não existe não grava nada — e diz qual é o campo.
func TestAulaInexistenteERecusada(t *testing.T) {
	h, _ := serveTrainingComAulas(t, 1500)
	corpo := `{"classId":"nao_existe","occurredAt":"2026-09-13T09:00:00Z","localDay":"2026-09-13",
	           "plannedSeconds":100,"durationSeconds":100,"setsDone":0}`
	w := postComChave(t, h, "/v1/training/sessions", corpo, "fantasma-1")
	if w.Code != http.StatusNotFound {
		t.Errorf("esperava 404, deu %d — %s", w.Code, w.Body.String())
	}
}

// serveTrainingComAulas monta o router de treino com um catálogo de aulas.
// serveTrainingComAulas monta o router com uma aula **a sério** na base.
//
// Com uma aula falsa a chave estrangeira recusava o registo — e com razão: uma
// sessão não pode apontar para uma aula que não existe.
func serveTrainingComAulas(t *testing.T, duracao int) (http.Handler, string) {
	t.Helper()
	_, pool, userID := serve(t)

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO workout_class
		   (id,title,specialist,focus,level,duration_seconds,kcal,video_url,published)
		 VALUES ('aula_x','Aula de teste','ana-silva','upper','intermediate',$1,300,'https://x/v.mp4',true)`,
		duracao); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	if _, err := catalog.SeedExercises(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog), training.DefaultConfig(),
		clock.NewFixed(time.Date(2026, 9, 13, 18, 0, 0, 0, time.UTC)))

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth: fakeAuth{userID: userID},
		Training: &handlers.Training{
			Service: svc, Profiles: trainingProfiles{}, Classes: repo.NewClassRepo(tx),
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), userID
}

func postComChave(t *testing.T, h http.Handler, path, body, chave string) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, h, path, body, map[string]string{"Idempotency-Key": chave})
}
