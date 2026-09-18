package http_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

func serveMedicoes(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	tx := repo.NewTxManager(pool)

	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:         fakeAuth{userID: userID},
		Measurements: &handlers.Measurements{Store: repo.NewProgressRepo(tx)},
		Idempotency:  middleware.NewMemoryStore(time.Hour),
	})
}

type medicaoJSON struct {
	ID         string  `json:"id"`
	Metric     string  `json:"metric"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Day        string  `json:"day"`
	Note       string  `json:"note"`
	RecordedAt string  `json:"recordedAt"`
}

func serie(t *testing.T, h http.Handler, query string) []medicaoJSON {
	t.Helper()
	w := get(t, h, "/v1/measurements"+query)
	if w.Code != http.StatusOK {
		t.Fatalf("ler série: %d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Measurements []medicaoJSON `json:"measurements"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Measurements
}

// O peso escrito sobrevive, e volta com a nota.
//
// Era isto que não existia: o ecrã de evolução guardava os pesos no telemóvel e
// uma reinstalação apagava-os. O servidor sabia a tendência e não sabia dizer
// por que pontos a pessoa tinha passado.
func TestPesoGravadoVoltaComANota(t *testing.T) {
	h := serveMedicoes(t)

	/*
	 * Com `unit`, que o cliente manda e nenhum teste mandava.
	 *
	 * ⚠️ Descoberto pelos tipos gerados do contrato: o telemóvel envia
	 * `unit: "kg"`, o servidor lê-o e devolve-o, e o contrato não o conhecia —
	 * porque o contrato é o que os testes mandam. Um campo que só o cliente
	 * usa é um campo que se pode apagar do servidor sem nada falhar aqui.
	 */
	corpo := `{"metric":"body_weight","value":78.4,"unit":"kg","recordedAt":"2026-09-10T08:00:00Z","note":"Depois do treino"}`
	w := post(t, h, "/v1/measurements", corpo, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}
	var criada struct {
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &criada); err != nil {
		t.Fatalf("resposta ilegível: %v", err)
	}
	if criada.Unit != "kg" {
		t.Errorf("a unidade não voltou: %q", criada.Unit)
	}

	pontos := serie(t, h, "?metric=body_weight&since=2026-01-01")
	if len(pontos) != 1 {
		t.Fatalf("%d pontos na série, esperava 1", len(pontos))
	}
	p := pontos[0]
	if p.Value != 78.4 || p.Unit != "kg" || p.Day != "2026-09-10" || p.Note != "Depois do treino" {
		t.Errorf("ponto devolvido: %+v", p)
	}
	if p.ID == "" {
		t.Error("ponto sem identificador: a lista não tem como o distinguir")
	}
}

// O mesmo ponto duas vezes continua a ser um ponto.
//
// O telemóvel reenvia o que ficou por enviar sem rede, e o peso do dia chega
// também pela gravação do perfil. Sem esta garantia, quem escreveu o peso uma
// vez via-o duas na lista.
func TestMesmoPesoNoMesmoDiaNaoDuplica(t *testing.T) {
	h := serveMedicoes(t)

	corpo := `{"metric":"body_weight","value":80,"recordedAt":"2026-09-11T07:00:00Z"}`
	if w := post(t, h, "/v1/measurements", corpo, nil); w.Code != http.StatusCreated {
		t.Fatalf("primeira: %d", w.Code)
	}
	// Outra hora do mesmo dia, mesmo valor: é a mesma medição.
	repetido := `{"metric":"body_weight","value":80,"recordedAt":"2026-09-11T21:30:00Z"}`
	w := post(t, h, "/v1/measurements", repetido, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("repetida devia dar 200 sem criar, deu %d — %s", w.Code, w.Body.String())
	}

	if pontos := serie(t, h, "?since=2026-01-01"); len(pontos) != 1 {
		t.Fatalf("%d pontos depois de repetir, esperava 1", len(pontos))
	}

	// Um valor diferente no mesmo dia **é** outra medição: quem se pesou antes
	// e depois do treino escreveu dois números, não um.
	outro := `{"metric":"body_weight","value":79.6,"recordedAt":"2026-09-11T21:31:00Z"}`
	if w := post(t, h, "/v1/measurements", outro, nil); w.Code != http.StatusCreated {
		t.Fatalf("valor diferente devia criar, deu %d", w.Code)
	}
	if pontos := serie(t, h, "?since=2026-01-01"); len(pontos) != 2 {
		t.Fatalf("%d pontos, esperava 2", len(pontos))
	}
}

// A série vem do mais recente para o mais antigo — a ordem por que se lê.
func TestSerieVemDoMaisRecenteParaOMaisAntigo(t *testing.T) {
	h := serveMedicoes(t)
	for _, c := range []string{
		`{"value":81,"recordedAt":"2026-09-01T08:00:00Z"}`,
		`{"value":80,"recordedAt":"2026-09-08T08:00:00Z"}`,
		`{"value":79,"recordedAt":"2026-09-15T08:00:00Z"}`,
	} {
		if w := post(t, h, "/v1/measurements", c, nil); w.Code != http.StatusCreated {
			t.Fatalf("gravar %s: %d", c, w.Code)
		}
	}
	pontos := serie(t, h, "?since=2026-01-01")
	if len(pontos) != 3 {
		t.Fatalf("%d pontos", len(pontos))
	}
	if pontos[0].Day != "2026-09-15" || pontos[2].Day != "2026-09-01" {
		t.Errorf("ordem errada: %s … %s", pontos[0].Day, pontos[2].Day)
	}
}

// Uma métrica que não existe é recusada com o campo, não com um 500.
func TestMetricaDesconhecidaERecusada(t *testing.T) {
	h := serveMedicoes(t)
	w := post(t, h, "/v1/measurements", `{"metric":"altura_da_alma","value":10}`, nil)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("esperava recusa, deu %d — %s", w.Code, w.Body.String())
	}
	if w := get(t, h, "/v1/measurements?metric=altura_da_alma"); w.Code == http.StatusOK {
		t.Error("ler uma métrica desconhecida devia ser recusado")
	}
}

// Gravar o perfil duas vezes não põe o peso duas vezes na lista.
//
// O perfil grava-se muito — editar o plano, acertar os minutos, cada descida
// que o telemóvel confirma — e cada gravação registava um ponto novo. Quem
// escreveu o peso uma vez via-o três vezes no mesmo dia.
func TestGravarOPerfilDuasVezesNaoDuplicaOPeso(t *testing.T) {
	_, pool, userID := serve(t)
	tx := repo.NewTxManager(pool)
	perfis := repo.NewProfileRepo(tx)
	progresso := repo.NewProgressRepo(tx)
	ctx := t.Context()

	quando := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		// A mesma pesagem, a horas diferentes do mesmo dia.
		if err := perfis.RecordWeight(ctx, userID, 78, quando.Add(time.Duration(i)*3*time.Hour)); err != nil {
			t.Fatalf("gravar peso %d: %v", i, err)
		}
	}

	serie, err := progresso.Series(ctx, userID, "body_weight", quando.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(serie) != 1 {
		t.Fatalf("%d pontos depois de três gravações do mesmo peso, esperava 1", len(serie))
	}

	// Um peso diferente no mesmo dia continua a ser outra pesagem.
	if err := perfis.RecordWeight(ctx, userID, 77.4, quando.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	serie, err = progresso.Series(ctx, userID, "body_weight", quando.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(serie) != 2 {
		t.Fatalf("%d pontos, esperava 2", len(serie))
	}
}
