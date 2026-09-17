package http_test

import (
	"context"
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
 * O treinador da equipa muda o treino — ponta a ponta.
 *
 * ⚠️ Escolher a Ana ou o Miguel era escolher um nome por baixo do título das
 * aulas: o plano saía exactamente igual. Uma escolha sem consequência é uma
 * pergunta a fingir.
 *
 * Este arnês junta o perfil, a equipa e o treino, que é o caminho verdadeiro:
 * a pessoa convida alguém e o treino do dia sai diferente.
 */
func serveComEquipa(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	if _, err := catalog.SeedExercises(context.Background()); err != nil {
		t.Fatal(err)
	}
	especialistas := repo.NewSpecialistRepo(tx)
	if _, err := especialistas.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}

	profiles := service.NewProfiles(repo.NewProfileRepo(tx), nil, repo.NewPreferenceRepo(tx)).
		ComEquipa(especialistas)
	fixed := clock.NewFixed(time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC))
	svc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog), training.DefaultConfig(), fixed)

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Profile:     &handlers.Profile{Profiles: profiles, Clock: fixed},
		Training:    &handlers.Training{Service: svc, Profiles: profiles},
		Specialists: &handlers.Specialists{Store: especialistas},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

func TestOTreinadorDaEquipaMudaOTreino(t *testing.T) {
	h := serveComEquipa(t)

	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}

	semNinguem := treinoDeHoje(t, h)

	// A pessoa convida a Ana — força e hipertrofia.
	if w := put(t, h, "/v1/specialists/team", `{"team":["ana-silva"]}`); w.Code != http.StatusOK {
		t.Fatalf("equipa: %d — %s", w.Code, w.Body.String())
	}
	comAna := treinoDeHoje(t, h)

	if semNinguem == comAna {
		t.Fatal("o treino saiu exactamente igual — a escolha não teve consequência")
	}

	/*
	 * E a diferença é a que se promete: menos exercícios, mais séries cada.
	 *
	 * Conta-se pelo rótulo de série mais alto que aparece — é o que a pessoa
	 * vê no ecrã, e o pacote não expõe os números em cru de propósito.
	 */
	maisAltaCom := serieMaisAlta(comAna)
	maisAltaSem := serieMaisAlta(semNinguem)
	if maisAltaCom <= maisAltaSem {
		t.Errorf("com a Ana a série mais alta é %d; sem ninguém é %d — não subiu",
			maisAltaCom, maisAltaSem)
	}
}

/** A série mais alta que o pacote menciona: "SÉRIE 3 DE 4" → 4. */
func serieMaisAlta(corpo string) int {
	mais := 0
	for _, parte := range strings.Split(corpo, "DE ") {
		if len(parte) == 0 {
			continue
		}
		n := int(parte[0] - '0')
		if n > 0 && n < 10 && n > mais {
			mais = n
		}
	}
	return mais
}

/*
 * Um nutricionista na equipa não mexe no treino.
 *
 * Muda o que se come; não acrescenta séries ao agachamento. Um método que
 * mudasse tudo por qualquer pessoa na equipa seria ruído, não método.
 */
func TestUmNutricionistaNaoMexeNoTreino(t *testing.T) {
	h := serveComEquipa(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d", w.Code)
	}

	antes := treinoDeHoje(t, h)
	if w := put(t, h, "/v1/specialists/team", `{"team":["carla-mendes"]}`); w.Code != http.StatusOK {
		t.Fatalf("equipa: %d — %s", w.Code, w.Body.String())
	}
	if depois := treinoDeHoje(t, h); depois != antes {
		t.Error("a nutricionista mexeu no treino")
	}
}

/*
 * O fisioterapeuta tira o impacto do treino.
 *
 * É o que ele faz na vida real, e é o que faz aqui.
 */
func TestOFisioterapeutaNaEquipaTiraOsSaltos(t *testing.T) {
	h := serveComEquipa(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d", w.Code)
	}
	if w := put(t, h, "/v1/specialists/team", `{"team":["diogo-ramos"]}`); w.Code != http.StatusOK {
		t.Fatalf("equipa: %d — %s", w.Code, w.Body.String())
	}

	treino := treinoDeHoje(t, h)
	for _, salto := range []string{"Polichinelos", "Burpee", "Agachamento com salto", "Saltos laterais"} {
		if strings.Contains(treino, salto) {
			t.Errorf("%q entrou num treino com o fisioterapeuta na equipa", salto)
		}
	}
	if !strings.Contains(treino, "sessionId") {
		t.Error("o treino ficou vazio")
	}
}
