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

	if _, err := repo.NewSpecialistRepo(repo.NewTxManager(pool)).Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO workout_class
		   (id,title,specialist,focus,level,duration_seconds,kcal,video_url,published)
		 VALUES ('aula_x','Aula de teste','ana-silva','full','intermediate',$1,300,'https://x/v.mp4',true)`,
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
			Service: svc, Profiles: trainingProfiles{},
			Classes: repo.NewClassRepo(tx), Training: training.DefaultConfig(),
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), userID
}

// serveTrainingComAulasDeFoco semeia uma aula de outro foco, para o dia não a
// poder usar.
func serveTrainingComAulasDeFoco(t *testing.T, foco string) (http.Handler, string) {
	t.Helper()
	_, pool, userID := serve(t)

	if _, err := repo.NewSpecialistRepo(repo.NewTxManager(pool)).Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO workout_class
		   (id,title,specialist,focus,level,duration_seconds,kcal,video_url,published)
		 VALUES ('aula_outra','Outra','ana-silva',$1::session_focus,'beginner',900,200,'https://x/v.mp4',true)`,
		foco); err != nil {
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
			Service: svc, Profiles: trainingProfiles{},
			Classes: repo.NewClassRepo(tx), Training: training.DefaultConfig(),
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), userID
}

func postComChave(t *testing.T, h http.Handler, path, body, chave string) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, h, path, body, map[string]string{"Idempotency-Key": chave})
}

// Quando há aula que serve o dia, o dia **é** a aula — e não as duas coisas.
//
// Dois treinos para o mesmo dia é exactamente a confusão que isto existe para
// resolver.
func TestODiaComAulaEAAula(t *testing.T) {
	h, _ := serveTrainingComAulas(t, 1500)
	w := get(t, h, "/v1/training/today?localDay=2026-09-13")
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}

	var env struct {
		Kind  string `json:"kind"`
		Class struct {
			ID              string `json:"id"`
			Title           string `json:"title"`
			DurationSeconds int    `json:"durationSeconds"`
		} `json:"class"`
		Session json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}

	// O perfil de teste é "Full Body" e intermédio; a aula semeada é `full`,
	// intermédia e sem equipamento — logo serve.
	if env.Kind != "class" {
		t.Fatalf("esperava uma aula, veio %q", env.Kind)
	}
	if env.Class.ID != "aula_x" || env.Class.DurationSeconds != 1500 {
		t.Errorf("aula errada: %+v", env.Class)
	}
	if len(env.Session) > 0 {
		t.Error("veio aula e plano ao mesmo tempo — é um dia, é um treino")
	}
}

// Sem aula que sirva o foco, o dia continua a ser o plano. É a salvaguarda que
// impede a Airo de marcar um dia de aula que não tem como encher.
func TestSemAulaQueSirvaODiaEOPlano(t *testing.T) {
	h, _ := serveTrainingComAulasDeFoco(t, "cardio")
	w := get(t, h, "/v1/training/today?localDay=2026-09-13")

	var env struct {
		Kind string `json:"kind"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	if env.Kind != "session" {
		t.Errorf("a única aula é de cardio e o dia é de tronco; veio %q", env.Kind)
	}
}
