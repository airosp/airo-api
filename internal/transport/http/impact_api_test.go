package http_test

import (
	"context"
	"encoding/json"
	"net/http"
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
)

/*
 * O arnês que junta os dois lados.
 *
 * Os outros dão ao treino um perfil de mentira, o que chega para provar o
 * motor. Aqui não chegava: o que está em causa é justamente o caminho entre o
 * que a pessoa grava no perfil e o que o motor lhe propõe.
 */
func servePerfilETreino(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	if _, err := catalog.SeedExercises(context.Background()); err != nil {
		t.Fatal(err)
	}
	profiles := service.NewProfiles(repo.NewProfileRepo(tx), nil, repo.NewPreferenceRepo(tx))
	fixed := clock.NewFixed(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	svc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog), training.DefaultConfig(), fixed)

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Profile:     &handlers.Profile{Profiles: profiles, Clock: fixed},
		Training:    &handlers.Training{Service: svc, Profiles: profiles},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

/*
 * O tecto de impacto: até onde o corpo pode bater no chão.
 *
 * Quem tem joelhos maus recebia polichinelos e burpees na mesma. A resposta
 * dessas pessoas não é reclamar — é deixar de abrir a app.
 */

func perfilComTecto(t *testing.T, h http.Handler, tecto string) map[string]any {
	t.Helper()
	corpo := `{"displayName":"Ensaio","age":30,"sex":"female","heightCm":165,"weightKg":62,
	           "experience":"beginner","workoutDays":[0,2,4],"workoutMinutes":30,
	           "workoutTime":"evening","equipment":[],"dietStyle":"omnivore",
	           "mealsPerDay":4,"foodBudget":"medium","foodExclusions":[]`
	if tecto != "—" {
		corpo += `,"maxImpact":"` + tecto + `"`
	}
	corpo += `}`

	w := put(t, h, "/v1/profile", corpo)
	if w.Code != http.StatusOK {
		t.Fatalf("gravar perfil: %d — %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("resposta ilegível: %v", err)
	}
	return out
}

func TestOTectoDeImpactoFicaGravado(t *testing.T) {
	h := servePerfilETreino(t)

	if got := perfilComTecto(t, h, "low")["maxImpact"]; got != "low" {
		t.Fatalf("gravou %v", got)
	}

	// Ler outra vez: o que ficou é o que se lê.
	w := get(t, h, "/v1/profile")
	if w.Code != http.StatusOK {
		t.Fatalf("ler perfil: %d", w.Code)
	}
	var lido map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &lido); err != nil {
		t.Fatal(err)
	}
	if lido["maxImpact"] != "low" {
		t.Errorf("leu %v", lido["maxImpact"])
	}
}

/*
 * Um campo ausente não apaga o tecto.
 *
 * É a razão de ser um ponteiro: uma app por actualizar que não conhece o campo
 * estaria a dizer "quero tudo" sem o querer dizer — e quem escolheu não saltar
 * voltava a receber saltos por ter mudado os minutos de treino.
 */
func TestGravarSemFalarNoTectoNaoOApaga(t *testing.T) {
	h := servePerfilETreino(t)
	perfilComTecto(t, h, "low")

	if got := perfilComTecto(t, h, "—")["maxImpact"]; got != "low" {
		t.Fatalf("o tecto passou a %v", got)
	}
}

// Uma cadeia vazia tira o tecto — e não rebenta com o enum.
func TestVazioTiraOTecto(t *testing.T) {
	h := servePerfilETreino(t)
	perfilComTecto(t, h, "low")

	out := perfilComTecto(t, h, "")
	if _, existe := out["maxImpact"]; existe {
		t.Fatalf("o tecto ficou: %v", out["maxImpact"])
	}
}

// Um nível que não existe é recusado no pedido, não pela base de dados.
func TestTectoDesconhecidoRecusado(t *testing.T) {
	h := servePerfilETreino(t)
	w := put(t, h, "/v1/profile",
		`{"displayName":"Ensaio","age":30,"sex":"female","heightCm":165,"weightKg":62,
		  "experience":"beginner","workoutDays":[0,2,4],"workoutMinutes":30,
		  "workoutTime":"evening","equipment":[],"dietStyle":"omnivore",
		  "mealsPerDay":4,"foodBudget":"medium","foodExclusions":[],"maxImpact":"nenhum"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("devolveu %d — %s", w.Code, w.Body.String())
	}
}

/*
 * Com tecto baixo, o treino do dia não traz saltos.
 *
 * É esta a prova que interessa: o campo gravado não vale nada se o motor
 * continuar a propor burpees. Procura-se pelo **nome** porque o pacote esconde
 * os identificadores de propósito — o cliente recebe decisões, não catálogo.
 */
func TestComTectoBaixoOTreinoNaoTemSaltos(t *testing.T) {
	h := servePerfilETreino(t)

	saltos := []string{"Polichinelos", "Joelhos ao peito", "Burpee",
		"Agachamento com salto", "Saltos laterais", "Intervalos na passadeira"}

	perfilComTecto(t, h, "—")
	semTecto := treinoDeHoje(t, h)

	perfilComTecto(t, h, "low")
	comTecto := treinoDeHoje(t, h)

	if comTecto == "" {
		t.Fatal("o treino ficou vazio — tirou-se de mais")
	}
	for _, nome := range saltos {
		if strings.Contains(comTecto, nome) {
			t.Errorf("%q entrou num treino de impacto baixo", nome)
		}
	}
	// A prova só vale se o treino sem tecto tivesse mesmo algum salto: senão
	// este teste passava com o motor avariado.
	tinha := false
	for _, nome := range saltos {
		if strings.Contains(semTecto, nome) {
			tinha = true
		}
	}
	if !tinha {
		t.Skip("o treino de hoje não tinha saltos para tirar")
	}
}

func treinoDeHoje(t *testing.T, h http.Handler) string {
	t.Helper()
	w := get(t, h, "/v1/training/today?localDay=2026-09-16")
	if w.Code != http.StatusOK {
		t.Fatalf("treino de hoje: %d — %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}
