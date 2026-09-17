package http_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

/*
 * O progresso, pela rota.
 *
 * ⚠️ `GET /v1/progress/snapshot` **não era tocado por teste nenhum** — uma das
 * 38 rotas sem cobertura. É a rota que decide a consistência que o ecrã de
 * início e o de progresso mostram, e a fase contra a qual a pessoa é medida.
 */
func serveProgresso(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	tx := repo.NewTxManager(pool)
	cfgs := service.Configs{
		Goal: goal.DefaultConfig(), Journey: journey.DefaultConfig(), Nutrition: nutrition.DefaultConfig(),
	}
	goals := repo.NewGoalRepo(tx)
	profiles := service.NewProfiles(repo.NewProfileRepo(tx), nil, repo.NewPreferenceRepo(tx))
	relogio := clock.NewFixed(agoraDeEnsaio)

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:    fakeAuth{userID: userID},
		Profile: &handlers.Profile{Profiles: profiles, Clock: relogio},
		Goals: &handlers.Goals{
			Service:  service.NewGoalService(tx, goals, cfgs, relogio),
			Profiles: profiles, Reader: goals, Editor: goals,
		},
		Progress: &handlers.Progress{
			Service: service.NewProgressService(repo.NewProgressRepo(tx), goals, cfgs.Journey).
				WithAdaptations(repo.NewAdaptationRepo(tx), goals, repo.NewProfileRepo(tx)).
				WithAbsences(repo.NewCalendarRepo(tx)),
			Profiles: profiles, Clock: relogio,
			Marks:    repo.NewCalendarRepo(tx),
			Sessions: repo.NewSessionRepo(tx, repo.NewCatalogRepo(tx)),
			Training: training.DefaultConfig(),
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

type snapshotJSON struct {
	Horizon   string `json:"horizon"`
	JourneyID string `json:"journeyId"`
	Phase     *struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		Index        int    `json:"index"`
		Title        string `json:"title"`
		Intent       string `json:"intent"`
		StartDateISO string `json:"startDateISO"`
		EndDateISO   string `json:"endDateISO"`
		Weeks        int    `json:"weeks"`
		Total        int    `json:"total"`
	} `json:"phase"`
	Adherence *struct {
		Overall   float64 `json:"overall"`
		Evaluable bool    `json:"evaluable"`
	} `json:"adherence"`
}

func snapshot(t *testing.T, h http.Handler) snapshotJSON {
	t.Helper()
	w := get(t, h, "/v1/progress/snapshot")
	if w.Code != http.StatusOK {
		t.Fatalf("snapshot: %d — %s", w.Code, w.Body.String())
	}
	var out snapshotJSON
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("resposta ilegível: %v — %s", err, w.Body.String())
	}
	return out
}

/*
 * Um objectivo com prazo tem fases, e a rota diz em qual vai.
 *
 * ⚠️ `currentPhase` corria no telemóvel em quatro sítios. A fase decide qual é
 * o plano em vigor — logo decide a adesão contra a qual a pessoa é medida —, e
 * quatro respostas possíveis à mesma pergunta é uma a mais.
 */
func TestOSnapshotDizEmQueFaseVai(t *testing.T) {
	h := serveProgresso(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	if w := post(t, h, "/v1/goals", fixedBody, nil); w.Code != http.StatusCreated {
		t.Fatalf("objectivo: %d — %s", w.Code, w.Body.String())
	}

	s := snapshot(t, h)
	if s.Phase == nil {
		t.Fatal("um objectivo com prazo tem fases, e a rota não disse nenhuma")
	}
	if s.Phase.ID == "" || s.Phase.Title == "" || s.Phase.Kind == "" {
		t.Errorf("fase incompleta: %+v", s.Phase)
	}
	if s.Phase.Index < 1 {
		t.Errorf("a fase começa a contar em 1, veio %d", s.Phase.Index)
	}
	// "Fase 2" sozinho não diz nada: a pergunta que se faz é "de quantas?".
	if s.Phase.Total < s.Phase.Index {
		t.Errorf("fase %d de %d — o total é menor do que o índice", s.Phase.Index, s.Phase.Total)
	}
	if s.Phase.Weeks < 1 {
		t.Errorf("fase sem semanas: %+v", s.Phase)
	}
	// A fase em curso contém o dia de hoje: se não contivesse, não era a em curso.
	inicio, err1 := time.Parse(time.RFC3339, s.Phase.StartDateISO)
	fim, err2 := time.Parse(time.RFC3339, s.Phase.EndDateISO)
	if err1 != nil || err2 != nil {
		t.Fatalf("datas da fase ilegíveis: %q / %q", s.Phase.StartDateISO, s.Phase.EndDateISO)
	}
	if agoraDeEnsaio.Before(inicio) || agoraDeEnsaio.After(fim) {
		t.Errorf("hoje (%s) está fora da fase em curso (%s → %s)",
			agoraDeEnsaio.Format("2006-01-02"), inicio.Format("2006-01-02"), fim.Format("2006-01-02"))
	}
}

/*
 * No primeiro dia não se julga ninguém; passados dias, julga-se.
 *
 * ⚠️ `evaluable` é a diferença entre "0% porque ainda não havia nada marcado" e
 * "0% porque não foste" — e é este o número que os dois ecrãs mostram como
 * "consistência". Sem a distinção, quem começou hoje via 0% e concluía que já
 * estava a falhar.
 *
 * A regra é `planeados > 0`, e planeados sai das semanas decorridas vezes a
 * frequência. Escrevi este teste a assumir que quatro dias não davam nada a
 * julgar — davam dois treinos marcados, e nenhum feito. O servidor tinha razão.
 */
func TestAConsistenciaSoJulgaDepoisDeHaverAlgoMarcado(t *testing.T) {
	h := serveProgresso(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}

	// Começado hoje: ainda não houve semana nenhuma, logo nada estava marcado.
	comecadoHoje := `{"type":"outcome","horizon":"fixed","direction":"lose_weight","priority":"weight",
	 "startDate":"` + agoraDeEnsaio.Format("2006-01-02") + `","targetDate":"2026-12-29",
	 "targets":[{"metric":"body_weight","value":74,"unit":"kg","direction":"decrease"}]}`
	if w := post(t, h, "/v1/goals", comecadoHoje, nil); w.Code != http.StatusCreated {
		t.Fatalf("objectivo: %d — %s", w.Code, w.Body.String())
	}

	if s := snapshot(t, h); s.Adherence != nil && s.Adherence.Evaluable {
		t.Errorf("no primeiro dia já se julga a adesão: %+v", s.Adherence)
	}
}

/*
 * Passados dias sem treinar, a consistência é zero — e conta.
 *
 * É o outro lado da moeda: não julgar no primeiro dia não pode virar não julgar
 * nunca, senão o número nunca diz nada a ninguém.
 */
func TestPassadosDiasSemTreinarAConsistenciaConta(t *testing.T) {
	h := serveProgresso(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	// `fixedBody` começa a 12/9 e o relógio de ensaio está em 16/9: quatro
	// dias, com treinos marcados pelo meio.
	if w := post(t, h, "/v1/goals", fixedBody, nil); w.Code != http.StatusCreated {
		t.Fatalf("objectivo: %d — %s", w.Code, w.Body.String())
	}

	s := snapshot(t, h)
	if s.Adherence == nil || !s.Adherence.Evaluable {
		t.Fatalf("passados quatro dias ainda não se julga: %+v", s.Adherence)
	}
	if s.Adherence.Overall != 0 {
		t.Errorf("sem nada feito a adesão devia ser zero, veio %v", s.Adherence.Overall)
	}
}

// Sem objectivo não há progresso a medir — e isso é o princípio, não um erro.
func TestSemObjectivoNaoHaProgresso(t *testing.T) {
	h := serveProgresso(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	if w := get(t, h, "/v1/progress/snapshot"); w.Code != http.StatusNotFound {
		t.Errorf("sem objectivo devolveu %d — %s", w.Code, w.Body.String())
	}
}
