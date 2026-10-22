package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	"github.com/airosp/airo-api/internal/platform/email"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/migrations"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type painelEnsaio struct {
	t      *testing.T
	h      http.Handler
	token  string
	userID string
	now    time.Time
	mail   *caixaDeCorreio
}

// caixaDeCorreio guarda os emails que o painel enviaria.
type caixaDeCorreio struct {
	mu  sync.Mutex
	out []email.Message
}

func (c *caixaDeCorreio) Send(_ context.Context, m email.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out = append(c.out, m)
	return nil
}

// ultimo devolve o último email para `to`.
func (c *caixaDeCorreio) ultimo(to string) (email.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.out) - 1; i >= 0; i-- {
		if c.out[i].To == to {
			return c.out[i], true
		}
	}
	return email.Message{}, false
}

// entrar faz os dois passos e devolve o token da sessão.
func (p *painelEnsaio) entrar(addr string) string {
	p.t.Helper()
	ch := p.must(200, "POST", "/v1/admin/auth/login", `{"email":"`+addr+`"}`)
	m, ok := p.mail.ultimo(strings.ToLower(addr))
	if !ok {
		p.t.Fatalf("nenhum código para %s", addr)
	}
	code := strings.Fields(m.Subject)[0]
	if !strings.Contains(m.HTML, code) {
		p.t.Fatal("o código vai no corpo do email")
	}
	ses := p.must(200, "POST", "/v1/admin/auth/verify", `{"challenge":"`+ch["challenge"].(string)+`","code":"`+code+`"}`)
	return ses["token"].(string)
}

func (p *painelEnsaio) do(method, path, body string, headers ...string) (int, map[string]any) {
	p.t.Helper()
	var rd *bytes.Buffer
	if body == "" {
		rd = &bytes.Buffer{}
	} else {
		rd = bytes.NewBufferString(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://painel.exemplo")
	if p.token != "" {
		r.Header.Set("Authorization", "Bearer "+p.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	p.h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (p *painelEnsaio) must(code int, method, path, body string, headers ...string) map[string]any {
	p.t.Helper()
	got, out := p.do(method, path, body, headers...)
	if got != code {
		b, _ := json.Marshal(out)
		p.t.Fatalf("%s %s → %d, queria %d: %s", method, path, got, code, b)
	}
	return out
}

func painelPronto(t *testing.T) *painelEnsaio {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(ctx, pool, migs, quietLogger()); err != nil {
		t.Fatal(err)
	}
	tx := repo.NewTxManager(pool)
	for _, seed := range []func(context.Context) (int, error){
		repo.NewCatalogRepo(tx).SeedExercises,
		repo.NewNutritionCatalogRepo(tx).SeedFoods,
		repo.NewNutritionCatalogRepo(tx).SeedRecipes,
		repo.NewSpecialistRepo(tx).Seed,
		repo.NewClassRepo(tx).Seed,
		repo.NewPlaylistRepo(tx).Seed,
	} {
		if _, err := seed(ctx); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		training.ResetLibrary()
	})

	// Uma pessoa da app com perfil, peso e um objectivo.
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841234567','MZ') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO profile (user_id, display_name, height_cm, experience, age_years, sex, profile_complete)
		 VALUES ($1, 'Marta Sousa', 168, 'beginner', 31, 'female', true)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO measurement (user_id, metric, value, unit, recorded_at) VALUES ($1,'body_weight',82,'kg', now() - interval '3 days')`,
		userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_specialist (user_id, specialist_id, role) SELECT $1, id, role FROM specialist WHERE role = 'trainer' LIMIT 1`,
		userID); err != nil {
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
	now := time.Now().UTC()
	log := quietLogger()
	mail := &caixaDeCorreio{}
	deps := airohttp.Wire(airohttp.Platform{
		Log: log, Version: "test", Pool: pool, Redis: rdb, Clock: clock.NewFixed(now),
		JWTSecret: secret, OTPPepper: pepper, Sender: airohttp.NewLogSender(log),
		PanelURL: "https://painel.exemplo", Mail: mail,
	})
	deps.CORSOrigins = []string{"https://painel.exemplo"}
	deps.Auth = fakeAuth{userID: userID}
	if err := deps.ReloadCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	h := airohttp.NewRouter(deps)

	// O objectivo nasce pela rota da app, como na vida real.
	p := &painelEnsaio{t: t, h: h, userID: userID, now: now}
	start := now.Format("2006-01-02")
	end := now.AddDate(0, 3, 0).Format("2006-01-02")
	p.token = "app"
	p.must(201, "POST", "/v1/goals", `{"type":"outcome","horizon":"fixed","direction":"lose_weight","priority":"weight",
	  "startDate":"`+start+`","targetDate":"`+end+`",
	  "targets":[{"metric":"body_weight","value":75,"unit":"kg","direction":"decrease"}]}`)
	p.token = ""

	// A primeira conta, como o subcomando a cria.
	if _, err := repo.NewAdminRepo(tx).AddMember(ctx, "ana@airo.test", "Ana Matos", "admin", nil); err != nil {
		t.Fatal(err)
	}
	p.mail = mail

	// Um email que não é da equipa recebe a mesma resposta, e nenhum email.
	fora := p.must(200, "POST", "/v1/admin/auth/login", `{"email":"intruso@airo.test"}`)
	if _, sent := mail.ultimo("intruso@airo.test"); sent || fora["challenge"] == nil {
		t.Fatal("um email de fora não recebe código, mas a resposta é igual")
	}
	p.must(401, "POST", "/v1/admin/auth/verify", `{"challenge":"`+fora["challenge"].(string)+`","code":"123456"}`)

	// Um código errado não entra; o certo sim, e só uma vez.
	ch := p.must(200, "POST", "/v1/admin/auth/login", `{"email":"ANA@airo.test"}`)
	m, _ := mail.ultimo("ana@airo.test")
	code := strings.Fields(m.Subject)[0]
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	p.must(401, "POST", "/v1/admin/auth/verify", `{"challenge":"`+ch["challenge"].(string)+`","code":"`+wrong+`"}`)
	ses := p.must(200, "POST", "/v1/admin/auth/verify", `{"challenge":"`+ch["challenge"].(string)+`","code":"`+code+`"}`)
	p.must(401, "POST", "/v1/admin/auth/verify", `{"challenge":"`+ch["challenge"].(string)+`","code":"`+code+`"}`)
	if again := p.entrar("ana@airo.test"); again == ses["token"] {
		t.Fatal("cada entrada é uma sessão nova")
	}
	p.token = ses["token"].(string)
	return p
}

func TestPainelDePontaAPonta(t *testing.T) {
	p := painelPronto(t)

	me := p.must(200, "GET", "/v1/admin/me", "")
	if me["permissions"].(map[string]any)["team"] != "edit" {
		t.Fatalf("a administração gere a equipa: %v", me["permissions"])
	}

	ov := p.must(200, "GET", "/v1/admin/overview?days=7", "")
	if ov["kpis"].(map[string]any)["totalPeople"].(float64) != 1 {
		t.Fatalf("visão geral: %v", ov["kpis"])
	}
	if len(ov["funnel"].([]any)) != 5 || len(ov["services"].([]any)) != 6 {
		t.Fatalf("visão geral incompleta: %v", ov)
	}

	us := p.must(200, "GET", "/v1/admin/users?q=marta", "")
	items := us["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("procura: %v", us)
	}
	first := items[0].(map[string]any)
	if strings.Contains(first["phoneMasked"].(string), "234") {
		t.Fatalf("o telefone tem de sair mascarado: %v", first["phoneMasked"])
	}
	if first["goal"] == nil || first["journey"] == nil || len(first["team"].([]any)) != 1 {
		t.Fatalf("linha incompleta: %v", first)
	}
	p.must(200, "GET", "/v1/admin/users?q=1234567", "")

	p.must(403, "GET", "/v1/admin/users/"+p.userID, "")
	rec := p.must(200, "GET", "/v1/admin/users/"+p.userID, "", "X-Admin-Reason", "Pedido de suporte")
	for _, k := range []string{"user", "profile", "goal", "weights", "trainingDays", "foodDays", "sessions", "adherence", "risks", "adaptations", "nutrition", "team"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("ficha sem %q", k)
		}
	}
	if len(rec["trainingDays"].([]any)) != 35 {
		t.Errorf("o calendário são cinco semanas")
	}
	if g := rec["goal"].(map[string]any); g["targetWeightKg"] == nil || g["currentWeightKg"] == nil {
		t.Errorf("objetivo sem pesos: %v", g)
	}

	// Especialistas.
	sp := p.must(201, "POST", "/v1/admin/specialists", `{"name":"Carla Vieira","role":"nutritionist","headline":"Nutrição desportiva","tags":["Perda de gordura"],"recommendedFor":["lose_fat"]}`)
	if sp["id"] != "carla_vieira" || sp["initials"] != "CV" {
		t.Fatalf("especialista: %v", sp)
	}
	p.must(200, "PUT", "/v1/admin/specialists/carla_vieira", `{"name":"Carla Vieira","role":"nutritionist","headline":"Nutrição clínica","maxClients":40}`)
	inv := p.must(201, "POST", "/v1/admin/specialists/carla_vieira/invite", `{"email":"carla@exemplo.pt"}`)
	if inv["emailSent"] != true || inv["member"].(map[string]any)["role"] != "specialist" {
		t.Fatalf("acesso do especialista: %v", inv)
	}

	// Aulas: publicar sem vídeo é recusado; o envio sem Mux diz que não há.
	cl := p.must(201, "POST", "/v1/admin/classes", `{"title":"Intervalos 20/40","specialistId":"carla_vieira","focus":"cardio","level":"beginner","durationSeconds":1200,"kcal":180,"muscles":["Pernas"],"policy":"signed"}`)
	id := cl["id"].(string)
	if cl["video"].(map[string]any)["status"] != "none" {
		t.Fatalf("aula nova sem vídeo: %v", cl["video"])
	}
	p.must(409, "POST", "/v1/admin/classes/"+id+"/publish", `{"published":true}`)
	p.must(503, "POST", "/v1/admin/classes/"+id+"/upload", `{"policy":"signed"}`)
	lst := p.must(200, "GET", "/v1/admin/classes", "")
	if len(lst["items"].([]any)) < 2 {
		t.Fatal("lista de aulas")
	}

	// Playlists com a aula nova e uma do catálogo.
	pls := p.must(200, "GET", "/v1/admin/playlists", "")
	pl0 := pls["items"].([]any)[0].(map[string]any)
	firstClass := pl0["items"].([]any)[0].(map[string]any)["classId"].(string)
	pl := p.must(201, "POST", "/v1/admin/playlists", `{"title":"Começar do zero","level":"beginner","goals":["lose_fat"]}`)
	pl = p.must(200, "PUT", "/v1/admin/playlists/"+pl["id"].(string), `{"title":"Começar do zero","level":"beginner","published":true,"items":[{"classId":"`+firstClass+`","subtitle":"Aquecimento"},{"classId":"`+id+`","subtitle":"Cardio"}]}`)
	if len(pl["items"].([]any)) != 2 || pl["totalSeconds"].(float64) < 1200 {
		t.Fatalf("playlist: %v", pl)
	}
	p.must(409, "DELETE", "/v1/admin/classes/"+id, "")

	// Exercícios: a edição chega ao motor.
	ex := p.must(200, "GET", "/v1/admin/exercises", "")
	if len(ex["items"].([]any)) != 60 {
		t.Fatalf("exercícios: %d", len(ex["items"].([]any)))
	}
	p.must(200, "PUT", "/v1/admin/exercises/pushup", `{"name":"Flexões","pattern":"push","muscles":["Peito","Tríceps"],"equipment":[],"level":"beginner","impact":"low","measure":"reps","cue":"Corpo em linha, sempre.","active":true}`)
	if e, _ := training.GetExercise("pushup"); e.Cue != "Corpo em linha, sempre." {
		t.Fatalf("o motor não recebeu a edição: %q", e.Cue)
	}
	p.must(201, "POST", "/v1/admin/exercises", `{"name":"Prancha lateral","pattern":"core","muscles":["Oblíquos"],"level":"beginner","impact":"low","measure":"time","cue":"Anca alta."}`)
	if _, ok := training.GetExercise("prancha_lateral"); !ok {
		t.Fatal("exercício novo fora do motor")
	}

	// Modelos de treino com a verificação.
	tpl := p.must(201, "POST", "/v1/admin/workout-templates", `{"name":"Corpo inteiro A","focus":"full","level":"beginner","minutes":10,
	  "blocks":{"warmup":[{"exerciseId":"pushup","sets":1,"reps":10,"restSeconds":30}],
	            "main":[{"exerciseId":"pushup","sets":3,"reps":12,"restSeconds":60}],
	            "cooldown":[{"exerciseId":"prancha_lateral","sets":1,"seconds":30,"restSeconds":0}]}}`)
	if len(tpl["checks"].([]any)) < 4 {
		t.Fatalf("verificação: %v", tpl["checks"])
	}
	p.must(200, "GET", "/v1/admin/exercises", "")

	// Pratos: o motor calcula, e um estilo incompatível é recusado.
	prev := p.must(200, "POST", "/v1/admin/recipes/preview", `{"ingredients":[{"foodId":"chicken","grams":150}],"serves":1}`)
	if prev["nutrition"].(map[string]any)["kcal"].(float64) != 248 {
		t.Fatalf("150 g de frango: %v", prev["nutrition"])
	}
	p.must(422, "POST", "/v1/admin/recipes", `{"name":"Frango grelhado","slots":["lunch"],"styles":["vegan"],"serves":1,"ingredients":[{"foodId":"chicken","grams":150}]}`)
	rc := p.must(201, "POST", "/v1/admin/recipes", `{"name":"Frango grelhado","slots":["lunch"],"styles":["omnivore"],"serves":1,"ingredients":[{"foodId":"chicken","grams":150}]}`)
	if rc["validatedBy"] != nil {
		t.Fatal("um prato novo nasce por validar")
	}
	rc = p.must(200, "POST", "/v1/admin/recipes/frango_grelhado/validate", "")
	if rc["validatedBy"] != "Ana Matos" {
		t.Fatalf("validação: %v", rc["validatedBy"])
	}
	p.must(422, "PUT", "/v1/admin/foods/chicken", `{"name":"Frango","category":"protein","kcal":900,"protein":31,"carbs":0,"fat":3.6,"serving":120,"budget":"medium","styles":["omnivore"]}`)
	p.must(200, "PUT", "/v1/admin/foods/chicken", `{"name":"Frango","category":"protein","kcal":170,"protein":31,"carbs":0,"fat":3.6,"serving":120,"budget":"medium","styles":["omnivore","high_protein"]}`)

	// Dietas: regras, semana, revisão.
	dm := p.must(201, "POST", "/v1/admin/diet-models", `{"name":"Perda de gordura 4 refeições","goal":"lose_fat","mealsPerDay":4,
	  "rules":{"deficitPct":15,"minKcal":1500,"proteinPerKg":1.8,"fatPct":25},
	  "week":[{"day":0,"meals":[{"slot":"lunch","recipeId":"frango_grelhado"}]}]}`)
	if dm["example"].(map[string]any)["kcal"].(float64) != 2040 {
		t.Fatalf("exemplo: %v", dm["example"])
	}
	p.must(422, "POST", "/v1/admin/diet-models", `{"name":"X","goal":"lose_fat","mealsPerDay":4,"rules":{"deficitPct":15,"minKcal":1200,"proteinPerKg":1.8,"fatPct":25}}`)
	p.must(200, "POST", "/v1/admin/diet-models/"+dm["id"].(string)+"/status", `{"status":"review","comment":"Ver o jantar"}`)
	ap := p.must(200, "POST", "/v1/admin/diet-models/"+dm["id"].(string)+"/status", `{"status":"approved"}`)
	if ap["approvedBy"] != "Ana Matos" || len(ap["comments"].([]any)) != 1 {
		t.Fatalf("aprovação: %v", ap)
	}

	// Equipa e auditoria.
	tm := p.must(200, "GET", "/v1/admin/team", "")
	if len(tm["members"].([]any)) != 2 {
		t.Fatalf("equipa: %v", tm["members"])
	}
	nv := p.must(201, "POST", "/v1/admin/team/invites", `{"email":"joana@airo.test","name":"Joana Silva","role":"support"}`)
	jid := nv["member"].(map[string]any)["id"].(string)
	p.must(200, "PATCH", "/v1/admin/team/"+jid, `{"role":"content"}`)
	me2 := me["member"].(map[string]any)["id"].(string)
	p.must(409, "PATCH", "/v1/admin/team/"+me2, `{"active":false}`)
	au := p.must(200, "GET", "/v1/admin/audit?limit=100", "")
	found := false
	for _, it := range au["items"].([]any) {
		m := it.(map[string]any)
		if m["action"] == "user_record_opened" && strings.Contains(m["detail"].(string), "Pedido de suporte") {
			found = true
		}
	}
	if !found {
		t.Fatal("abrir a ficha tem de ficar na auditoria, com o motivo")
	}

	// O CORS deixa passar o cabeçalho do motivo.
	r := httptest.NewRequest("OPTIONS", "/v1/admin/users/x", nil)
	r.Header.Set("Origin", "https://painel.exemplo")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	p.h.ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-Admin-Reason") {
		t.Fatal("o CORS tem de aceitar X-Admin-Reason")
	}

	// Sair fecha a sessão.
	p.must(204, "POST", "/v1/admin/auth/logout", "")
	p.must(401, "GET", "/v1/admin/me", "")
}

// O suporte vê, não muda; e a ficha de saúde não abre a quem não é dela.
func TestPainelPermissoes(t *testing.T) {
	p := painelPronto(t)
	nv := p.must(201, "POST", "/v1/admin/team/invites", `{"email":"rui@airo.test","name":"Rui","role":"content"}`)
	if nv["emailSent"] != true {
		t.Fatal("quem é adicionado recebe um aviso por email")
	}
	if w, _ := p.mail.ultimo("rui@airo.test"); !strings.Contains(w.HTML, "https://painel.exemplo/entrar") {
		t.Fatal("o aviso leva o link do painel")
	}
	admin := p.token
	p.token = p.entrar("rui@airo.test")
	p.must(403, "GET", "/v1/admin/users/"+p.userID, "", "X-Admin-Reason", "Curiosidade a mais")
	p.must(403, "GET", "/v1/admin/team", "")
	p.must(200, "GET", "/v1/admin/classes", "")
	p.must(403, "POST", "/v1/admin/recipes/matapa_com_xima/validate", "")
	p.token = admin
	p.must(200, "GET", "/v1/admin/team", "")

	// Terminar as sessões de alguém deixa-o de fora até pedir outro código.
	rui := nv["member"].(map[string]any)["id"].(string)
	tokRui := p.entrar("rui@airo.test")
	p.must(200, "POST", "/v1/admin/team/"+rui+"/signout", "")
	p.token = tokRui
	p.must(401, "GET", "/v1/admin/me", "")
}
