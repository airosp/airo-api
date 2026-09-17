package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/migrations"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

/*
 * Toca em todas as rotas de leitura, e grava o que elas devolvem.
 *
 * ⚠️ O contrato cobria vinte e seis operações de setenta e seis, porque só
 * existia onde alguém tinha escrito um teste. Escrever cinquenta testes à mão
 * para fechar o buraco era um dia de trabalho a produzir uma cobertura que
 * voltava a envelhecer à primeira rota nova.
 *
 * Isto lê as rotas do **router** — a mesma fonte que gera a especificação — e
 * chama cada `GET`. Uma rota nova entra aqui sem ninguém fazer nada, e o
 * contrato dela fica gravado no mesmo instante em que ela nasce.
 *
 * Não verifica nada. É de propósito: o que valida cada rota são os testes que
 * já existem, e um teste que afirmasse "todas devolvem 200" ou seria mentira
 * ou obrigaria a montar o mundo inteiro para cada uma. Este existe para
 * **gravar a forma**, e diz em voz alta quantas não conseguiu.
 */
func TestContratoDeTodasAsRotasDeLeitura(t *testing.T) {
	h := routerComTudoLigado(t)

	// Um mundo mínimo: sem perfil, metade das rotas responde 422 e não há
	// forma nenhuma para gravar.
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil: %d — %s", w.Code, w.Body.String())
	}
	if w := post(t, h, "/v1/goals", fixedBody, nil); w.Code != http.StatusCreated {
		t.Logf("objetivo: %d — as rotas de progresso vão ficar sem forma", w.Code)
	}

	// Os identificadores que existem, lidos das próprias rotas de listagem.
	catalogoDeEnsaio["class"] = primeiroID(t, h, "/v1/classes?all=1", "classes")
	catalogoDeEnsaio["playlist"] = primeiroID(t, h, "/v1/playlists", "playlists")

	comForma, semForma := 0, 0
	var faltam []string

	for _, r := range rotasDoRouter(t) {
		if r.metodo != http.MethodGet {
			// Um POST ou DELETE às cegas escreve ou apaga. As formas de
			// escrita vêm dos testes que sabem o que estão a mandar.
			continue
		}
		caminho := preencher(r.caminho)
		w := get(t, h, caminho)
		if w.Code >= 200 && w.Code < 300 && w.Body.Len() > 0 {
			comForma++
			continue
		}
		semForma++
		faltam = append(faltam, fmt.Sprintf("%s %s → %d", r.metodo, r.caminho, w.Code))
	}

	sort.Strings(faltam)
	t.Logf("rotas de leitura: %d com forma gravada, %d sem", comForma, semForma)
	for _, f := range faltam {
		t.Logf("  sem forma: %s", f)
	}
}

type rotaLida struct{ metodo, caminho string }

/*
 * rotasDoRouter lê as rotas do ficheiro que as regista.
 *
 * O `http.ServeMux` não enumera os padrões que conhece, e uma lista à mão ao
 * lado dele nasce desactualizada — já aconteceu neste repositório, e faltavam
 * sete rotas que o cliente chamava todos os dias.
 */
func rotasDoRouter(t *testing.T) []rotaLida {
	t.Helper()
	bruto, err := os.ReadFile(filepath.Join("router.go"))
	if err != nil {
		t.Fatalf("ler o router: %v", err)
	}
	padrao := regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/[^"]*)"`)

	vistas := map[string]bool{}
	var out []rotaLida
	for _, m := range padrao.FindAllStringSubmatch(string(bruto), -1) {
		chave := m[1] + " " + m[2]
		if vistas[chave] {
			continue
		}
		vistas[chave] = true
		out = append(out, rotaLida{metodo: m[1], caminho: m[2]})
	}
	return out
}

/*
 * preencher troca os parâmetros do caminho por valores que existem.
 *
 * Um `{id}` inventado devolve 404 e não grava forma nenhuma; os valores aqui
 * são os que o mundo mínimo deste teste cria. Onde não há valor bom, fica um
 * qualquer — e a rota aparece na lista das que ficaram sem forma, que é o
 * sítio certo para ela aparecer.
 */
func preencher(caminho string) string {
	hoje := time.Now().UTC()

	/*
	 * O `{id}` quer dizer uma coisa diferente em cada rota.
	 *
	 * Um valor único para todas dava 404 em três delas — e um 404 não tem
	 * forma para gravar. Cada família leva um identificador que o seed cria.
	 */
	identificador := "chicken"
	switch {
	case strings.Contains(caminho, "/v1/exercises/"):
		identificador = "pushup"
	case strings.Contains(caminho, "/v1/classes/"):
		identificador = primeiroDoCatalogo("class")
	case strings.Contains(caminho, "/v1/playlists/"):
		identificador = primeiroDoCatalogo("playlist")
	}

	trocas := strings.NewReplacer(
		"{day}", hoje.Format("2006-01-02"),
		"{week}", hoje.Format("2006-01-02"),
		"{id}", identificador,
		"{position}", "1",
	)
	caminho = trocas.Replace(caminho)

	// As consultas obrigatórias: sem elas a rota recusa antes de responder.
	switch {
	case strings.HasSuffix(caminho, "/v1/training/today"):
		return caminho + "?localDay=" + hoje.Format("2006-01-02")
	case strings.HasSuffix(caminho, "/v1/training/sessions"),
		strings.HasSuffix(caminho, "/v1/measurements"),
		strings.HasSuffix(caminho, "/v1/hydration"),
		strings.HasSuffix(caminho, "/v1/calendar/marks"),
		strings.HasSuffix(caminho, "/v1/nutrition/logs"):
		return caminho + "?from=" + hoje.AddDate(0, 0, -30).Format("2006-01-02") +
			"&to=" + hoje.Format("2006-01-02")
	case strings.HasSuffix(caminho, "/v1/nutrition/today"):
		return caminho + "?localDay=" + hoje.Format("2006-01-02")
	case strings.HasSuffix(caminho, "/v1/media/videos"),
		strings.HasSuffix(caminho, "/v1/media/photos"):
		return caminho + "?query=treino"
	}
	return caminho
}

/*
 * routerComTudoLigado é o router de produção com a autenticação de ensaio.
 *
 * O `routerCompleto` dos outros testes usa a autenticação a sério, e por isso
 * responde 401 a tudo — serve para verificar quem exige sessão, não para
 * gravar formas.
 */
func routerComTudoLigado(t *testing.T) http.Handler {
	t.Helper()
	pool := pgtest.Pool(t)
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(t.Context(), pool, migs, quietLogger()); err != nil {
		t.Fatal(err)
	}

	var userID string
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258849990100','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	secret := make([]byte, 32)
	pepper := make([]byte, 32)
	for i := range secret {
		secret[i], pepper[i] = byte(i+1), byte(i+100)
	}
	log := quietLogger()
	deps := airohttp.Wire(airohttp.Platform{
		Log: log, Version: "test", Pool: pool, Redis: rdb,
		Clock:                 clock.NewFixed(time.Now().UTC()),
		JWTSecret:             secret,
		OTPPepper:             pepper,
		Sender:                airohttp.NewLogSender(log),
		WhatsAppWebhookSecret: "segredo-de-ensaio",
	})
	deps.Schema = airohttp.SchemaState{Migrations: migs, Pool: pool}
	// A autenticação de ensaio entra no lugar da verdadeira: o que se quer
	// gravar são as formas, e um 401 não tem forma nenhuma.
	deps.Auth = fakeAuth{userID: userID}

	tx := repo.NewTxManager(pool)
	catalog := repo.NewCatalogRepo(tx)
	if _, err := catalog.SeedExercises(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.NewSpecialistRepo(tx).Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	// As aulas e as listas: sem elas, `/v1/classes/{id}` e `/v1/playlists/{id}`
	// respondem 404 e não há forma nenhuma para gravar.
	if _, err := repo.NewClassRepo(tx).Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.NewPlaylistRepo(tx).Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Uma sessão gravada, para o histórico e o detalhe terem o que devolver.
	svc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog),
		training.DefaultConfig(), clock.NewFixed(time.Now().UTC()))
	_ = svc

	deps.Idempotency = middleware.NewMemoryStore(time.Hour)
	_ = handlers.Health{}
	return airohttp.NewRouter(deps)
}

/*
 * primeiroDoCatalogo devolve um identificador que existe mesmo.
 *
 * Preenchido pelo próprio teste ao ler a lista da rota de listagem: inventar
 * um identificador aqui era voltar a ter uma lista à mão a envelhecer ao lado
 * do que existe.
 */
var catalogoDeEnsaio = map[string]string{}

func primeiroDoCatalogo(tipo string) string {
	if id, ok := catalogoDeEnsaio[tipo]; ok {
		return id
	}
	return "nao-existe"
}

/** O primeiro identificador de uma lista, ou vazio quando a lista o é. */
func primeiroID(t *testing.T, h http.Handler, caminho, campo string) string {
	t.Helper()
	w := get(t, h, caminho)
	if w.Code != http.StatusOK {
		return "nao-existe"
	}
	/*
	 * Lê-se **só o campo que interessa**, e não o corpo inteiro.
	 *
	 * Um `map[string][]…` obriga todos os valores de topo a ser listas, e
	 * `/v1/classes` devolve `total` ao lado de `classes`: o unmarshal falhava
	 * por inteiro e a função devolvia "não existe" com a lista cheia à frente.
	 */
	var corpo map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &corpo); err != nil {
		return "nao-existe"
	}
	var itens []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(corpo[campo], &itens); err != nil || len(itens) == 0 {
		return "nao-existe"
	}
	return itens[0].ID
}
