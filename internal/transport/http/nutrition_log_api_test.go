package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/migrations"
)

func serveDiario(t *testing.T) http.Handler {
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
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258849998887','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	tx := repo.NewTxManager(pool)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Nutrition:   &handlers.Nutrition{Logs: repo.NewNutritionRepo(tx)},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

func registo(dia string, kcal int) string {
	return fmt.Sprintf(`{
	  "slot":"lunch","status":"custom","portion":1,"kcal":%d,
	  "macros":{"protein":30,"carbs":60,"fat":18},
	  "source":"photo","label":"Massa com tomate","portionLabel":"1 prato",
	  "photoUrl":"https://res.cloudinary.test/x/w_1024/prato.jpg",
	  "photoThumbUrl":"https://res.cloudinary.test/x/w_256/prato.jpg",
	  "recordedAt":"%sT12:30:00Z","localDay":"%s"
	}`, kcal, dia, dia)
}

func gravar(t *testing.T, h http.Handler, id, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/v1/nutrition/logs/"+id, strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	// Esta não passa pelos ajudantes comuns, e por isso grava aqui.
	gravarContrato(http.MethodPut, "/v1/nutrition/logs/"+id, corpo, w)
	return w
}

func ler(t *testing.T, h http.Handler, de, ate string) dto.MealLogsResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/nutrition/logs?from="+de+"&to="+ate, nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	var out dto.MealLogsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// O diário sobrevive: grava-se e lê-se de volta, inteiro.
func TestDiarioGravaELeDeVolta(t *testing.T) {
	h := serveDiario(t)

	if w := gravar(t, h, "log_1789279256", registo("2026-09-13", 520)); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}

	out := ler(t, h, "2026-09-13", "2026-09-13")
	if len(out.Logs) != 1 {
		t.Fatalf("%d registos", len(out.Logs))
	}
	l := out.Logs[0]
	if l.ID != "log_1789279256" {
		t.Fatalf("o identificador do telemóvel tem de voltar: %q", l.ID)
	}
	if l.Kcal != 520 || l.Macros.Protein != 30 || l.Label != "Massa com tomate" {
		t.Fatalf("registo = %+v", l)
	}
	// A fotografia é o que torna o registo mais do que um número — e era ela
	// que ficava órfã enquanto o diário só vivia no telemóvel.
	if !strings.Contains(l.PhotoURL, "w_1024") || !strings.Contains(l.PhotoThumbURL, "w_256") {
		t.Fatalf("fotografias = %q / %q", l.PhotoURL, l.PhotoThumbURL)
	}
	if l.PortionLabel != "1 prato" || l.Source != "photo" {
		t.Fatalf("registo = %+v", l)
	}
}

// Reenviar o mesmo registo é mandar o mesmo registo.
//
// É o caso normal, não um erro: o registo nasce offline e o telemóvel tenta
// outra vez quando a rede volta. Sem isto, o almoço aparecia três vezes.
func TestReenviarNaoDuplica(t *testing.T) {
	h := serveDiario(t)

	for i := 0; i < 4; i++ {
		if w := gravar(t, h, "log_mesmo", registo("2026-09-13", 520)); w.Code != http.StatusOK {
			t.Fatalf("envio %d = %d: %s", i, w.Code, w.Body.String())
		}
	}
	if out := ler(t, h, "2026-09-13", "2026-09-13"); len(out.Logs) != 1 {
		t.Fatalf("%d registos depois de quatro envios", len(out.Logs))
	}

	// E corrigir o que se registou actualiza, em vez de acrescentar: quem
	// escreveu 520 e depois 610 comeu uma vez.
	if w := gravar(t, h, "log_mesmo", registo("2026-09-13", 610)); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	out := ler(t, h, "2026-09-13", "2026-09-13")
	if len(out.Logs) != 1 || out.Logs[0].Kcal != 610 {
		t.Fatalf("registos = %d, kcal = %d", len(out.Logs), out.Logs[0].Kcal)
	}
}

// O intervalo é por dia **da pessoa**, e inclusive nas duas pontas.
func TestIntervaloDeDias(t *testing.T) {
	h := serveDiario(t)
	for i, dia := range []string{"2026-09-11", "2026-09-12", "2026-09-13", "2026-09-14"} {
		if w := gravar(t, h, fmt.Sprintf("log_%d", i), registo(dia, 400+i)); w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
	}
	if out := ler(t, h, "2026-09-12", "2026-09-13"); len(out.Logs) != 2 {
		t.Fatalf("%d registos no intervalo de dois dias", len(out.Logs))
	}
	if out := ler(t, h, "2026-09-11", "2026-09-14"); len(out.Logs) != 4 {
		t.Fatalf("%d registos nos quatro dias", len(out.Logs))
	}
}

// Valores fora do que o esquema aceita saem 422 com o campo — não 500.
func TestValoresInvalidosDao422(t *testing.T) {
	h := serveDiario(t)
	base := registo("2026-09-13", 520)

	casos := []struct{ nome, de, para, campo string }{
		{"refeição", `"slot":"lunch"`, `"slot":"brunch"`, "slot"},
		{"estado", `"status":"custom"`, `"status":"inventado"`, "status"},
		{"origem", `"source":"photo"`, `"source":"magia"`, "source"},
		{"fracção acima de 1", `"portion":1`, `"portion":1.5`, "portion"},
		{"fracção negativa", `"portion":1`, `"portion":-0.2`, "portion"},
		{"dia", `"localDay":"2026-09-13"`, `"localDay":"13/09/2026"`, "localDay"},
		{"momento", `"recordedAt":"2026-09-13T12:30:00Z"`, `"recordedAt":"ontem"`, "recordedAt"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			w := gravar(t, h, "log_x", strings.Replace(base, c.de, c.para, 1))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("= %d, esperava 422: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"field":"`+c.campo+`"`) {
				t.Fatalf("devia apontar o campo %s: %s", c.campo, w.Body.String())
			}
		})
	}
}

// Zero é um registo válido: "não comi" não é "não registei".
func TestPorcaoZeroEValida(t *testing.T) {
	h := serveDiario(t)
	corpo := strings.Replace(registo("2026-09-13", 0), `"portion":1`, `"portion":0`, 1)
	corpo = strings.Replace(corpo, `"status":"custom"`, `"status":"skipped"`, 1)
	if w := gravar(t, h, "log_saltado", corpo); w.Code != http.StatusOK {
		t.Fatalf("= %d: %s", w.Code, w.Body.String())
	}
	out := ler(t, h, "2026-09-13", "2026-09-13")
	if len(out.Logs) != 1 || out.Logs[0].Status != "skipped" || out.Logs[0].Portion != 0 {
		t.Fatalf("registo = %+v", out.Logs)
	}
}

// Apagar tira o registo, e apagar o que já não está responde o mesmo.
func TestApagarRegisto(t *testing.T) {
	h := serveDiario(t)
	if w := gravar(t, h, "log_1", registo("2026-09-13", 520)); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	apagar := func(id string) int {
		r := httptest.NewRequest(http.MethodDelete, "/v1/nutrition/logs/"+id, nil)
		r.Header.Set("Authorization", "Bearer token-de-teste")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := apagar("log_1"); code != http.StatusNoContent {
		t.Fatalf("apagar = %d", code)
	}
	if out := ler(t, h, "2026-09-13", "2026-09-13"); len(out.Logs) != 0 {
		t.Fatalf("%d registos depois de apagar", len(out.Logs))
	}
	if code := apagar("log_1"); code != http.StatusNoContent {
		t.Fatalf("apagar outra vez = %d, devia ser igual", code)
	}
	if code := apagar("nunca-existiu"); code != http.StatusNoContent {
		t.Fatalf("apagar o que não existe = %d", code)
	}
}

// Um intervalo enorme é um pedido que fica cada vez mais lento à medida que a
// pessoa usa a app.
func TestIntervaloDemasiadoGrande(t *testing.T) {
	h := serveDiario(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/nutrition/logs?from=2020-01-01&to=2026-12-31", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("= %d, esperava 422", w.Code)
	}
}
