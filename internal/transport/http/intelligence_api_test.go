package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/airosp/airo-api/internal/intelligence"
	"github.com/airosp/airo-api/internal/intelligence/policy"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	ai "github.com/airosp/airo-api/internal/service/intelligence"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fornecedorFalso responde o que o teste mandar, e guarda o que recebeu.
type fornecedorFalso struct {
	mu       sync.Mutex
	responde func(req core.StructuredRequest, image []byte) (core.StructuredResult, error)
	pedidos  []core.StructuredRequest
	imagens  [][]byte
}

func (f *fornecedorFalso) GenerateStructured(_ context.Context, req core.StructuredRequest) (core.StructuredResult, error) {
	f.mu.Lock()
	f.pedidos = append(f.pedidos, req)
	f.mu.Unlock()
	return f.responde(req, nil)
}

func (f *fornecedorFalso) AnalyzeImage(_ context.Context, req core.ImageRequest) (core.StructuredResult, error) {
	f.mu.Lock()
	f.pedidos = append(f.pedidos, req.StructuredRequest)
	f.imagens = append(f.imagens, req.Image)
	f.mu.Unlock()
	return f.responde(req.StructuredRequest, req.Image)
}

func ok(data string) func(core.StructuredRequest, []byte) (core.StructuredResult, error) {
	return func(core.StructuredRequest, []byte) (core.StructuredResult, error) {
		return core.StructuredResult{Data: []byte(data), Model: "modelo-teste", RequestID: "req_1",
			InputTokens: 2000, OutputTokens: 150}, nil
	}
}

// fotosFalsas aceita só os endereços da pasta de quem pede.
type fotosFalsas struct{}

func (fotosFalsas) MealAnalysisURL(url, userID string) (string, error) {
	if !strings.HasPrefix(url, "https://res.cloudinary.test/airo/meals/"+userID+"/") {
		return "", ai.ErrPhotoNotOwned
	}
	return url + "?analise", nil
}

func (fotosFalsas) Fetch(context.Context, string) ([]byte, string, error) {
	return []byte{0xff, 0xd8, 0xff, 0xe0}, "image/jpeg", nil
}

type mundoIA struct {
	h       http.Handler
	svc     *ai.Service
	pool    *pgxpool.Pool
	user    string
	prov    *fornecedorFalso
	relogio *clock.Fixed
}

func serveIA(t *testing.T, prov *fornecedorFalso, ajusta func(*ai.Config)) mundoIA {
	t.Helper()
	_, pool, userID := serve(t)
	tx := repo.NewTxManager(pool)
	relogio := clock.NewFixed(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	cfg := ai.DefaultConfig()
	cfg.Features = policy.Features{Enabled: true, MealPhoto: true, MealText: true}
	cfg.Prices = policy.Prices{InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10}
	cfg.Budget.MonthlyMicroUSD = 50_000_000
	if ajusta != nil {
		ajusta(&cfg)
	}
	var provider core.Provider
	if prov != nil {
		provider = prov
	}
	svc, err := ai.New(repo.NewAIRepo(tx), repo.NewNutritionRepo(tx), fotosFalsas{}, provider, relogio, quietLogger(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:         fakeAuth{userID: userID},
		Intelligence: &handlers.Intelligence{Service: svc},
		Nutrition:    &handlers.Nutrition{Logs: repo.NewNutritionRepo(tx)},
		Idempotency:  middleware.NewMemoryStore(time.Hour),
	})
	return mundoIA{h: h, svc: svc, pool: pool, user: userID, prov: prov, relogio: relogio}
}

func autorizar(t *testing.T, m mundoIA, purpose string) {
	t.Helper()
	body := `{"purpose":"` + purpose + `","granted":true,"policyVersion":"` + policy.PolicyVersions[purpose] + `"}`
	if w := put(t, m.h, "/v1/intelligence/consent", body); w.Code != http.StatusOK {
		t.Fatalf("autorizar: %d — %s", w.Code, w.Body.String())
	}
}

func submeter(t *testing.T, m mundoIA, path, body, chave string) (int, map[string]any) {
	t.Helper()
	w := post(t, m.h, path, body, map[string]string{"Idempotency-Key": chave})
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func lerJSON(t *testing.T, w interface{ Bytes() []byte }, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Bytes(), dst); err != nil {
		t.Fatalf("%v — %s", err, w.Bytes())
	}
}

type proposta struct {
	ProposalID string `json:"proposalId"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
	Draft      struct {
		Source   string `json:"source"`
		PhotoURL string `json:"photoUrl"`
		Items    []struct {
			FoodID      string   `json:"foodId"`
			Grams       *float64 `json:"grams"`
			GramsSource string   `json:"gramsSource"`
			Suggested   float64  `json:"suggestedGrams"`
			Per100g     struct {
				Kcal float64 `json:"kcal"`
			} `json:"per100g"`
		} `json:"items"`
		Unknown  []struct{ Label string } `json:"unknownItems"`
		Warnings []string                 `json:"warnings"`
	} `json:"draft"`
}

// correr reserva e corre a próxima execução, e devolve o estado da que se pediu.
func correr(t *testing.T, m mundoIA, jobID string) map[string]any {
	t.Helper()
	m.svc.RunOnce(context.Background())
	w := get(t, m.h, "/v1/intelligence/jobs/"+jobID)
	if w.Code != http.StatusOK {
		t.Fatalf("ler execução: %d — %s", w.Code, w.Body.String())
	}
	var out map[string]any
	lerJSON(t, w.Body, &out)
	return out
}

const xiaTexto = `{"containsFood":true,"items":[
 {"label":"xima","foodId":"xima","recipeId":"none","quantity":{"amount":null,"unit":"none"},"estimatedGrams":null},
 {"label":"matapa","foodId":"matapa","recipeId":"none","quantity":{"amount":null,"unit":"none"},"estimatedGrams":null},
 {"label":"peixe grelhado","foodId":"fish_local","recipeId":"none","quantity":{"amount":150,"unit":"g"},"estimatedGrams":null}]}`

// O percurso inteiro: autorizar, escrever, rever, confirmar — e o diário fica
// com os números do motor, não do modelo.
func TestRegistoPorTextoDePontaAPonta(t *testing.T) {
	prov := &fornecedorFalso{responde: ok(xiaTexto)}
	m := serveIA(t, prov, nil)

	w := get(t, m.h, "/v1/intelligence/capabilities")
	var caps ai.Capabilities
	lerJSON(t, w.Body, &caps)
	if !caps.Enabled || !caps.MealText.Available || caps.MealText.Consented {
		t.Fatalf("capacidades antes de autorizar: %+v", caps)
	}

	autorizar(t, m, policy.PurposeMealText)
	code, sub := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"Comi xima com matapa e 150 g de peixe grelhado. Liga-me: 841234567","slot":"lunch","localDay":"2026-10-22"}`,
		"chave-texto-0001")
	if code != http.StatusAccepted || sub["status"] != "queued" {
		t.Fatalf("submeter: %d %v", code, sub)
	}
	jobID := sub["jobId"].(string)

	estado := correr(t, m, jobID)
	if estado["status"] != "ready_for_review" || estado["proposalId"] == nil {
		t.Fatalf("estado: %v", estado)
	}

	// O que saiu da Airo: o texto sem o número de telefone, e nada de quem é.
	pedido := string(prov.pedidos[0].Payload)
	if strings.Contains(pedido, "841234567") || strings.Contains(pedido, m.user) {
		t.Errorf("saiu o que não devia: %s", pedido)
	}
	if !strings.Contains(prov.pedidos[0].Instructions, "xima: Xima") {
		t.Error("as instruções levam o catálogo")
	}

	var p proposta
	lerJSON(t, get(t, m.h, "/v1/intelligence/proposals/"+estado["proposalId"].(string)).Body, &p)
	if len(p.Draft.Items) != 3 {
		t.Fatalf("itens: %+v", p.Draft.Items)
	}
	if p.Draft.Items[0].Grams != nil || p.Draft.Items[0].GramsSource != "missing" {
		t.Errorf("xima sem quantidade dita fica por preencher: %+v", p.Draft.Items[0])
	}
	if g := p.Draft.Items[2].Grams; g == nil || *g != 150 {
		t.Errorf("peixe: %+v", p.Draft.Items[2])
	}

	// A pessoa preenche o que faltava e confirma.
	apply := `{"version":` + itoa(p.Version) + `,"logId":"ai_log_0001","slot":"lunch","localDay":"2026-10-22",
	           "recordedAt":"2026-10-22T12:05:00Z",
	           "items":[{"foodId":"xima","grams":250},{"foodId":"matapa","grams":180},{"foodId":"fish_local","grams":150}]}`
	w2 := post(t, m.h, "/v1/intelligence/proposals/"+p.ProposalID+"/apply", apply, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("confirmar: %d — %s", w2.Code, w2.Body.String())
	}
	var log ai.AppliedLog
	lerJSON(t, w2.Body, &log)
	var esperado float64
	for _, it := range []struct {
		id string
		g  float64
	}{{"xima", 250}, {"matapa", 180}, {"fish_local", 150}} {
		for _, d := range p.Draft.Items {
			if d.FoodID == it.id {
				esperado += d.Per100g.Kcal * it.g / 100
			}
		}
	}
	if diff := float64(log.Kcal) - esperado; diff > 0.5 || diff < -0.5 {
		t.Errorf("kcal %d, o catálogo dá %.1f", log.Kcal, esperado)
	}
	if log.Source != "manual" || log.Label == "" || log.PortionLabel != "580 g" {
		t.Errorf("registo: %+v", log)
	}

	// Está no diário, pelo mesmo caminho dos outros registos.
	var diario struct {
		Logs []struct {
			ID   string `json:"id"`
			Kcal int    `json:"kcal"`
		} `json:"logs"`
	}
	lerJSON(t, get(t, m.h, "/v1/nutrition/logs?from=2026-10-22&to=2026-10-22").Body, &diario)
	if len(diario.Logs) != 1 || diario.Logs[0].ID != "ai_log_0001" || diario.Logs[0].Kcal != log.Kcal {
		t.Fatalf("diário: %+v", diario.Logs)
	}

	// A mesma confirmação outra vez não grava outra refeição.
	w3 := post(t, m.h, "/v1/intelligence/proposals/"+p.ProposalID+"/apply", apply, nil)
	var replay ai.AppliedLog
	lerJSON(t, w3.Body, &replay)
	if w3.Code != http.StatusOK || !replay.AlreadyApplied {
		t.Fatalf("reenvio: %d %s", w3.Code, w3.Body.String())
	}
	// Outra confirmação, com outro registo, é recusada.
	outra := strings.Replace(apply, "ai_log_0001", "ai_log_0002", 1)
	if w4 := post(t, m.h, "/v1/intelligence/proposals/"+p.ProposalID+"/apply", outra, nil); w4.Code != http.StatusConflict {
		t.Fatalf("segunda confirmação: %d %s", w4.Code, w4.Body.String())
	}
	lerJSON(t, get(t, m.h, "/v1/nutrition/logs?from=2026-10-22&to=2026-10-22").Body, &diario)
	if len(diario.Logs) != 1 {
		t.Fatalf("o diário ficou com %d registos", len(diario.Logs))
	}

	// O consumo ficou contado e o texto saiu da execução.
	var custo int64
	var entrada string
	if err := m.pool.QueryRow(context.Background(),
		`SELECT u.cost_micro_usd, j.input_ref::text FROM ai_usage u JOIN ai_job j ON j.id = u.job_id WHERE j.id = $1`,
		jobID).Scan(&custo, &entrada); err != nil {
		t.Fatal(err)
	}
	if custo != 4000 { // 2000×1,25 + 150×10 = 4000 milionésimos
		t.Errorf("custo %d", custo)
	}
	if strings.Contains(entrada, "xima") {
		t.Errorf("o texto ficou guardado: %s", entrada)
	}
	var auditoria string
	_ = m.pool.QueryRow(context.Background(), `SELECT string_agg(detail::text, ' ') FROM ai_audit_event WHERE user_id = $1`, m.user).Scan(&auditoria)
	if strings.Contains(auditoria, "matapa") || strings.Contains(auditoria, "Comi") {
		t.Errorf("a auditoria guarda conteúdo: %s", auditoria)
	}
}

func TestFotografiaDePontaAPonta(t *testing.T) {
	prov := &fornecedorFalso{responde: ok(`{"containsFood":true,"items":[
	  {"label":"matapa com xima","foodId":"none","recipeId":"matapa_com_xima","quantity":{"amount":null,"unit":"none"},"estimatedGrams":300},
	  {"label":"molho picante","foodId":"none","recipeId":"none","quantity":{"amount":null,"unit":"none"},"estimatedGrams":20}]}`)}
	m := serveIA(t, prov, nil)
	autorizar(t, m, policy.PurposeMealPhoto)

	foto := "https://res.cloudinary.test/airo/meals/" + m.user + "/abc.jpg"
	code, sub := submeter(t, m, "/v1/intelligence/meals/recognize",
		`{"photoUrl":"`+foto+`","photoThumbUrl":"`+foto+`","slot":"dinner","localDay":"2026-10-22"}`, "chave-foto-0001")
	if code != http.StatusAccepted {
		t.Fatalf("%d %v", code, sub)
	}
	estado := correr(t, m, sub["jobId"].(string))
	if estado["status"] != "ready_for_review" {
		t.Fatalf("%v", estado)
	}
	if len(prov.imagens) != 1 || len(prov.imagens[0]) != 4 {
		t.Fatal("a imagem tem de ir ao fornecedor")
	}

	var p proposta
	lerJSON(t, get(t, m.h, "/v1/intelligence/proposals/"+estado["proposalId"].(string)).Body, &p)
	if p.Draft.Source != "photo" || p.Draft.PhotoURL != foto || len(p.Draft.Items) != 3 || len(p.Draft.Unknown) != 1 {
		t.Fatalf("rascunho: %+v", p.Draft)
	}
	if !contem(p.Draft.Warnings, "PORTIONS_ESTIMATED") || !contem(p.Draft.Warnings, "UNKNOWN_ITEMS") {
		t.Errorf("avisos %v", p.Draft.Warnings)
	}

	// A fotografia de outra pessoa não entra.
	alheia := "https://res.cloudinary.test/airo/meals/outra-pessoa/abc.jpg"
	if code, _ := submeter(t, m, "/v1/intelligence/meals/recognize",
		`{"photoUrl":"`+alheia+`","slot":"dinner","localDay":"2026-10-22"}`, "chave-foto-0002"); code != http.StatusUnprocessableEntity {
		t.Fatalf("fotografia alheia: %d", code)
	}
}

func contem(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func TestSemAutorizacaoNaoCorre(t *testing.T) {
	m := serveIA(t, &fornecedorFalso{responde: ok(xiaTexto)}, nil)
	code, out := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"arroz","slot":"lunch","localDay":"2026-10-22"}`, "chave-sem-auto")
	if code != http.StatusForbidden || out["error"].(map[string]any)["code"] != "ai_consent_required" {
		t.Fatalf("%d %v", code, out)
	}
	// Autorizar um texto antigo não vale.
	w := put(t, m.h, "/v1/intelligence/consent", `{"purpose":"meal_text","granted":true,"policyVersion":"2020-01-01"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("versão antiga: %d", w.Code)
	}
	// E uma finalidade sem texto publicado também não.
	w = put(t, m.h, "/v1/intelligence/consent", `{"purpose":"progress","granted":true,"policyVersion":"x"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("finalidade por publicar: %d", w.Code)
	}
}

func TestDesligadaDizQueEstaDesligada(t *testing.T) {
	m := serveIA(t, nil, nil) // sem fornecedor
	var caps ai.Capabilities
	lerJSON(t, get(t, m.h, "/v1/intelligence/capabilities").Body, &caps)
	if caps.Enabled || caps.MealPhoto.Available || caps.MealText.Available {
		t.Fatalf("%+v", caps)
	}
	autorizar(t, m, policy.PurposeMealText)
	code, out := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"arroz","slot":"lunch","localDay":"2026-10-22"}`, "chave-desligada")
	if code != http.StatusServiceUnavailable || out["error"].(map[string]any)["code"] != "ai_unavailable" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestMesmaChaveMesmaExecucaoELimiteDiario(t *testing.T) {
	m := serveIA(t, &fornecedorFalso{responde: ok(xiaTexto)}, func(c *ai.Config) { c.Budget.DailyPerUser = 2 })
	autorizar(t, m, policy.PurposeMealText)
	corpo := `{"text":"arroz com feijão","slot":"lunch","localDay":"2026-10-22"}`

	_, a := submeter(t, m, "/v1/intelligence/meals/parse", corpo, "chave-repetida-1")
	_, b := submeter(t, m, "/v1/intelligence/meals/parse", corpo, "chave-repetida-1")
	if a["jobId"] != b["jobId"] {
		t.Fatalf("o reenvio criou outra execução: %v %v", a, b)
	}
	if code, _ := submeter(t, m, "/v1/intelligence/meals/parse", corpo, "chave-repetida-2"); code != http.StatusAccepted {
		t.Fatalf("segundo pedido: %d", code)
	}
	code, out := submeter(t, m, "/v1/intelligence/meals/parse", corpo, "chave-repetida-3")
	if code != http.StatusTooManyRequests || out["error"].(map[string]any)["code"] != "ai_daily_limit" {
		t.Fatalf("terceiro pedido: %d %v", code, out)
	}
	if code, _ := submeter(t, m, "/v1/intelligence/meals/parse", corpo, ""); code != http.StatusUnprocessableEntity {
		t.Fatalf("sem chave: %d", code)
	}
}

func TestOrcamentoEsgotadoParaAsAnalises(t *testing.T) {
	m := serveIA(t, &fornecedorFalso{responde: ok(xiaTexto)}, func(c *ai.Config) { c.Budget.MonthlyMicroUSD = 1000 })
	autorizar(t, m, policy.PurposeMealText)
	if _, err := m.pool.Exec(context.Background(),
		`INSERT INTO ai_usage (task_type, model, cost_micro_usd, outcome) VALUES ('meal_text','x',1000,'ok')`); err != nil {
		t.Fatal(err)
	}
	code, out := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"arroz","slot":"lunch","localDay":"2026-10-22"}`, "chave-orcamento")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("%d %v", code, out)
	}
}

func TestRevogarCancelaOQueEstavaNaFila(t *testing.T) {
	prov := &fornecedorFalso{responde: ok(xiaTexto)}
	m := serveIA(t, prov, nil)
	autorizar(t, m, policy.PurposeMealText)
	_, sub := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"arroz com feijão","slot":"lunch","localDay":"2026-10-22"}`, "chave-revogar")

	if w := put(t, m.h, "/v1/intelligence/consent", `{"purpose":"meal_text","granted":false}`); w.Code != http.StatusOK {
		t.Fatalf("revogar: %d %s", w.Code, w.Body.String())
	}
	estado := correr(t, m, sub["jobId"].(string))
	if estado["status"] != "cancelled" || len(prov.pedidos) != 0 {
		t.Fatalf("depois de revogar: %v, %d chamadas", estado, len(prov.pedidos))
	}
}

func TestFalhasDoFornecedor(t *testing.T) {
	var n int
	prov := &fornecedorFalso{}
	prov.responde = func(req core.StructuredRequest, _ []byte) (core.StructuredResult, error) {
		n++
		if n == 1 {
			return core.StructuredResult{}, &core.ProviderError{Kind: core.Transient, Status: 503, Msg: "em baixo"}
		}
		return ok(xiaTexto)(req, nil)
	}
	m := serveIA(t, prov, nil)
	autorizar(t, m, policy.PurposeMealText)
	_, sub := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"xima e matapa","slot":"lunch","localDay":"2026-10-22"}`, "chave-falha-1")
	jobID := sub["jobId"].(string)

	if estado := correr(t, m, jobID); estado["status"] != "queued" || estado["errorCode"] != "provider_unavailable" {
		t.Fatalf("uma falha passageira volta à fila: %v", estado)
	}
	// Antes do recuo passar, não corre.
	if m.svc.RunOnce(context.Background()) {
		t.Fatal("correu antes do recuo")
	}
	m.relogio.Advance(10 * time.Second)
	if estado := correr(t, m, jobID); estado["status"] != "ready_for_review" {
		t.Fatalf("à segunda: %v", estado)
	}

	// Uma resposta fora do contrato não vira proposta.
	prov.responde = ok(`{"containsFood":true,"items":[],"kcal":900}`)
	_, sub = submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"arroz","slot":"lunch","localDay":"2026-10-22"}`, "chave-falha-2")
	if estado := correr(t, m, sub["jobId"].(string)); estado["status"] != "failed" || estado["errorCode"] != "invalid_output" {
		t.Fatalf("resposta inválida: %v", estado)
	}
}

func TestPropostaVelhaOuFechada(t *testing.T) {
	m := serveIA(t, &fornecedorFalso{responde: ok(xiaTexto)}, nil)
	autorizar(t, m, policy.PurposeMealText)
	_, sub := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"xima e matapa","slot":"lunch","localDay":"2026-10-22"}`, "chave-velha-1")
	estado := correr(t, m, sub["jobId"].(string))
	id := estado["proposalId"].(string)

	base := `,"logId":"ai_log_v","slot":"lunch","localDay":"2026-10-22","recordedAt":"2026-10-22T12:05:00Z","items":[{"foodId":"xima","grams":200}]}`
	if w := post(t, m.h, "/v1/intelligence/proposals/"+id+"/apply", `{"version":7`+base, nil); w.Code != http.StatusConflict {
		t.Fatalf("versão errada: %d", w.Code)
	}
	if w := post(t, m.h, "/v1/intelligence/proposals/"+id+"/apply",
		`{"version":1,"logId":"ai_log_v","slot":"lunch","localDay":"2026-10-22","recordedAt":"2026-10-22T12:05:00Z","items":[{"foodId":"pizza","grams":200}]}`, nil); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("alimento fora do catálogo: %d", w.Code)
	}
	if w := post(t, m.h, "/v1/intelligence/proposals/"+id+"/dismiss", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("dispensar: %d", w.Code)
	}
	if w := post(t, m.h, "/v1/intelligence/proposals/"+id+"/dismiss", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("dispensar outra vez: %d", w.Code)
	}
	if w := post(t, m.h, "/v1/intelligence/proposals/"+id+"/apply", `{"version":2`+base, nil); w.Code != http.StatusConflict {
		t.Fatalf("confirmar depois de dispensar: %d", w.Code)
	}
	// A proposta de outra pessoa não existe para esta.
	if w := get(t, m.h, "/v1/intelligence/proposals/00000000-0000-0000-0000-000000000000"); w.Code != http.StatusNotFound {
		t.Fatalf("%d", w.Code)
	}
}

// Um identificador de registo que já existe não é escrito por cima.
func TestConfirmarNaoPisaOutroRegisto(t *testing.T) {
	m := serveIA(t, &fornecedorFalso{responde: ok(xiaTexto)}, nil)
	autorizar(t, m, policy.PurposeMealText)
	if w := put(t, m.h, "/v1/nutrition/logs/log_existente", `{"slot":"lunch","status":"eaten","portion":1,"kcal":700,
	   "macros":{"protein":30,"carbs":80,"fat":20},"source":"plan","recordedAt":"2026-10-22T12:00:00Z","localDay":"2026-10-22"}`); w.Code != http.StatusOK {
		t.Fatalf("registo prévio: %d %s", w.Code, w.Body.String())
	}
	_, sub := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"xima","slot":"lunch","localDay":"2026-10-22"}`, "chave-pisar-1")
	estado := correr(t, m, sub["jobId"].(string))
	w := post(t, m.h, "/v1/intelligence/proposals/"+estado["proposalId"].(string)+"/apply",
		`{"version":1,"logId":"log_existente","slot":"lunch","localDay":"2026-10-22","recordedAt":"2026-10-22T12:05:00Z","items":[{"foodId":"xima","grams":200}]}`, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// Dois toques em "confirmar" ao mesmo tempo gravam uma refeição.
func TestConfirmarEmSimultaneoGravaUmaVez(t *testing.T) {
	m := serveIA(t, &fornecedorFalso{responde: ok(xiaTexto)}, nil)
	autorizar(t, m, policy.PurposeMealText)
	_, sub := submeter(t, m, "/v1/intelligence/meals/parse",
		`{"text":"xima","slot":"lunch","localDay":"2026-10-22"}`, "chave-simultaneo")
	id := correr(t, m, sub["jobId"].(string))["proposalId"].(string)

	var wg sync.WaitGroup
	codigos := make([]int, 6)
	for i := range codigos {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logID := "ai_log_s" + itoa(i)
			w := post(t, m.h, "/v1/intelligence/proposals/"+id+"/apply",
				`{"version":1,"logId":"`+logID+`","slot":"lunch","localDay":"2026-10-22","recordedAt":"2026-10-22T12:05:00Z","items":[{"foodId":"xima","grams":200}]}`, nil)
			codigos[i] = w.Code
		}(i)
	}
	wg.Wait()
	var n int
	if err := m.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM nutrition_log WHERE user_id = $1`, m.user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d registos gravados (respostas %v)", n, codigos)
	}
}

// Vários executores na mesma fila: cada execução corre uma vez.
func TestExecutoresNaoRepetemTrabalho(t *testing.T) {
	prov := &fornecedorFalso{responde: ok(xiaTexto)}
	m := serveIA(t, prov, nil)
	autorizar(t, m, policy.PurposeMealText)
	for i := 0; i < 8; i++ {
		submeter(t, m, "/v1/intelligence/meals/parse",
			`{"text":"xima","slot":"lunch","localDay":"2026-10-22"}`, "chave-fila-000"+itoa(i))
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m.svc.RunOnce(context.Background()) {
			}
		}()
	}
	wg.Wait()
	if len(prov.pedidos) != 8 {
		t.Fatalf("%d chamadas ao fornecedor para 8 execuções", len(prov.pedidos))
	}
	var prontas int
	_ = m.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ai_job WHERE user_id = $1 AND status = 'ready_for_review'`, m.user).Scan(&prontas)
	if prontas != 8 {
		t.Fatalf("%d prontas", prontas)
	}
}
