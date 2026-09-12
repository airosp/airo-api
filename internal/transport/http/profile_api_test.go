package http_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/migrations"
)

func serveProfile(t *testing.T) (http.Handler, string) {
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
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841112223','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	profiles := service.NewProfiles(repo.NewProfileRepo(tx))
	fixed := clock.NewFixed(time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))

	router := airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Profile:     &handlers.Profile{Profiles: profiles, Clock: fixed},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
	return router, userID
}

func put(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const perfilValido = `{
  "displayName":"Sandra","birthDate":"1996-04-02","sex":"female",
  "heightCm":168,"weightKg":72.4,"experience":"beginner",
  "workoutDays":[0,2,4],"workoutMinutes":35,"workoutTime":"evening",
  "equipment":["dumbbell"],"dietStyle":"omnivore","mealsPerDay":4,
  "foodBudget":"medium","foodExclusions":["amendoim"]
}`

// O caminho que faltava: o perfil não tinha por onde ser escrito, e sem ele
// nenhuma rota privada dava resposta útil.
func TestGravarPerfilEVoltarALer(t *testing.T) {
	h, _ := serveProfile(t)

	w := put(t, h, "/v1/profile", perfilValido)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}

	var saved service.SavedProfile
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.DisplayName != "Sandra" {
		t.Fatalf("nome = %q", saved.DisplayName)
	}
	// A idade sai da data de nascimento, e conta anos completos: a 13/09/2026
	// quem nasceu a 02/04/1996 tem 30.
	if saved.Age == nil || *saved.Age != 30 {
		t.Fatalf("idade = %v, esperava 30", saved.Age)
	}
	if saved.WeightKg != 72.4 {
		t.Fatalf("peso = %v", saved.WeightKg)
	}
	if !saved.Complete {
		t.Fatal("gravar o perfil devia marcá-lo completo")
	}

	// E lê-se de volta igual — se o `Save` gravasse noutro sítio, isto apanhava.
	r := httptest.NewRequest(http.MethodGet, "/v1/profile", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w2.Code, w2.Body.String())
	}
	var read service.SavedProfile
	if err := json.Unmarshal(w2.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.DisplayName != saved.DisplayName || read.WeightKg != saved.WeightKg ||
		read.MealsPerDay != 4 || read.WorkoutMinutes != 35 {
		t.Fatalf("lido ≠ gravado: %+v", read)
	}
	if len(read.FoodExclusions) != 1 || read.FoodExclusions[0] != "amendoim" {
		t.Fatalf("exclusões = %v", read.FoodExclusions)
	}
}

// Gravar duas vezes actualiza — não cria uma segunda linha nem rebenta na
// chave primária.
func TestGravarDuasVezesActualiza(t *testing.T) {
	h, _ := serveProfile(t)

	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("primeiro PUT = %d: %s", w.Code, w.Body.String())
	}
	segundo := strings.Replace(perfilValido, `"workoutMinutes":35`, `"workoutMinutes":50`, 1)
	w := put(t, h, "/v1/profile", segundo)
	if w.Code != http.StatusOK {
		t.Fatalf("segundo PUT = %d: %s", w.Code, w.Body.String())
	}

	var saved service.SavedProfile
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.WorkoutMinutes != 50 {
		t.Fatalf("minutos = %d, devia ter actualizado", saved.WorkoutMinutes)
	}
}

// Cada valor fora da lista do esquema tem de sair 422 com o campo — e não 500.
// Um enum recusado pela base de dados chega ao cliente como "a culpa é nossa".
func TestValoresForaDaListaDao422ComOCampo(t *testing.T) {
	h, _ := serveProfile(t)

	casos := []struct{ nome, body, campo string }{
		{"sexo", `"sex":"female"`, "sex"},
		{"experiência", `"experience":"beginner"`, "experience"},
		{"altura do dia", `"workoutTime":"evening"`, "workoutTime"},
		{"estilo alimentar", `"dietStyle":"omnivore"`, "dietStyle"},
		{"orçamento", `"foodBudget":"medium"`, "foodBudget"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			trocado := strings.Replace(perfilValido, c.body,
				`"`+c.campo+`":"nao_existe"`, 1)
			w := put(t, h, "/v1/profile", trocado)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s = %d, esperava 422: %s", c.campo, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"field":"`+c.campo+`"`) {
				t.Fatalf("a resposta tem de dizer o campo: %s", w.Body.String())
			}
		})
	}

	// `mealsPerDay` tem CHECK entre 3 e 5 no esquema. Sem validar, 6 saía 500.
	w := put(t, h, "/v1/profile", strings.Replace(perfilValido, `"mealsPerDay":4`, `"mealsPerDay":6`, 1))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("6 refeições = %d, esperava 422: %s", w.Code, w.Body.String())
	}
}

// Sem perfil, o GET diz que não existe — em vez de 500 ou de um objecto vazio
// que a app desenharia como se fosse real.
func TestSemPerfilOGetDiz404(t *testing.T) {
	h, _ := serveProfile(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/profile", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET sem perfil = %d, esperava 404: %s", w.Code, w.Body.String())
	}
}

// O perfil sem pesagem existe. Recusá-lo fecharia o treino a quem ainda não se
// pesou — e o treino é o que traz a pessoa de volta para se pesar.
func TestPerfilSemPesoContinuaALerSe(t *testing.T) {
	h, _ := serveProfile(t)
	semPeso := strings.Replace(perfilValido, `"weightKg":72.4,`, ``, 1)
	if w := put(t, h, "/v1/profile", semPeso); w.Code != http.StatusOK {
		t.Fatalf("PUT sem peso = %d: %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/profile", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	var read service.SavedProfile
	_ = json.Unmarshal(w.Body.Bytes(), &read)
	if read.WeightKg != 0 {
		t.Fatalf("sem pesagem o peso devia vir a zero; veio %v", read.WeightKg)
	}
	if read.DisplayName != "Sandra" {
		t.Fatal("o resto do perfil tem de vir na mesma")
	}
}
