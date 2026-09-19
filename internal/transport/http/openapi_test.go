package http_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/migrations"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

/*
 * A especificação não pode ficar atrás do router.
 *
 * ⚠️ Os tipos do cliente são escritos à mão ao lado dos do servidor, e custou um
 * `422`: o cliente mandava `targetKg` onde o servidor lê `targetWeightKg`. Uma
 * especificação que envelhece em silêncio é pior do que nenhuma — dá confiança
 * sem a merecer.
 *
 * Este teste pega em cada caminho de `docs/openapi.json` e pergunta ao router
 * se ele existe. Um 404 quer dizer que a especificação fala de uma rota que já
 * não há.
 */
func TestAEspecificacaoNaoFalaDeRotasQueNaoExistem(t *testing.T) {
	doc := lerEspecificacao(t)
	router := routerCompleto(t)

	for caminho, item := range doc.Paths {
		for metodo := range item {
			// Os parâmetros do caminho ganham um valor qualquer: o que se
			// pergunta é se a rota existe, não o que ela responde.
			pedido := strings.NewReplacer("{id}", "x", "{day}", "2026-09-16",
				"{week}", "2026-09-14", "{position}", "1").Replace(caminho)

			r := httptest.NewRequest(strings.ToUpper(metodo), pedido, http.NoBody)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			if w.Code == http.StatusNotFound {
				t.Errorf("%s %s está na especificação e o router não a conhece",
					strings.ToUpper(metodo), caminho)
			}
		}
	}
}

/*
 * E o contrário: uma rota privada que a especificação diga ser pública seria
 * uma promessa errada a quem gerar um cliente a partir dela.
 */
func TestAEspecificacaoDizQuaisExigemSessao(t *testing.T) {
	doc := lerEspecificacao(t)
	router := routerCompleto(t)

	for caminho, item := range doc.Paths {
		for metodo, op := range item {
			if strings.HasPrefix(caminho, "/v1/auth/") || strings.HasPrefix(caminho, "/v1/webhooks/") {
				continue // a entrada e os webhooks autenticam-se de outra forma
			}
			pedido := strings.NewReplacer("{id}", "x", "{day}", "2026-09-16",
				"{week}", "2026-09-14", "{position}", "1").Replace(caminho)

			r := httptest.NewRequest(strings.ToUpper(metodo), pedido, http.NoBody)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)

			exigeSessao := w.Code == http.StatusUnauthorized
			dizQueExige := len(op.Security) > 0

			if exigeSessao != dizQueExige {
				t.Errorf("%s %s: o router %s sessão, a especificação diz que %s",
					strings.ToUpper(metodo), caminho,
					seNao(exigeSessao, "exige", "não exige"),
					seNao(dizQueExige, "exige", "não exige"))
			}
		}
	}
}

func seNao(b bool, sim, nao string) string {
	if b {
		return sim
	}
	return nao
}

type especificacao struct {
	Paths map[string]map[string]struct {
		Security []any `json:"security"`
	} `json:"paths"`
}

func lerEspecificacao(t *testing.T) especificacao {
	t.Helper()
	bruto, err := os.ReadFile(filepath.Join("..", "..", "..", "openapi.json"))
	if err != nil {
		t.Skipf("sem openapi.json — corre `go run ./cmd/openapi > openapi.json`: %v", err)
	}
	var doc especificacao
	if err := json.Unmarshal(bruto, &doc); err != nil {
		t.Fatalf("especificação ilegível: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("especificação sem caminhos")
	}
	return doc
}

/** O router com tudo ligado — é o único contra o qual a comparação faz sentido. */
func routerCompleto(t *testing.T) http.Handler {
	t.Helper()
	pool := pgtest.Pool(t)
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if err := airopg.Up(t.Context(), pool, migs, quietLogger()); err != nil {
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
		Clock:                 clock.NewFixed(time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)),
		JWTSecret:             secret,
		OTPPepper:             pepper,
		Sender:                airohttp.NewLogSender(log),
		WhatsAppWebhookSecret: "segredo-de-ensaio",
		// Como o da Meta: sem segredo a rota não se regista, e a
		// especificação passaria a falar de uma rota que este router não tem.
		MuxWebhookSecret: "segredo-de-ensaio",
	})
	deps.Schema = airohttp.SchemaState{Migrations: migs, Pool: pool}
	return airohttp.NewRouter(deps)
}

/*
 * A especificação no disco é a que o gerador faria agora.
 *
 * ⚠️ Apanhei-me a olhar para uma `openapi.json` de ontem a dizer que a lista de
 * compras não tem preços, com o contrato gravado a dizer que tem — porque o
 * gerador escreve para o `stdout` e naquele dia o `stdout` foi para um `tail`.
 * Uma especificação velha é pior do que nenhuma: uma que falta vê-se, uma que
 * mente é lida e acreditada.
 *
 * Correr o gerador é a única comparação honesta — qualquer regra escrita aqui
 * ao lado dele seria uma terceira cópia a envelhecer sozinha.
 */
func TestEspecificacaoEstaEmDia(t *testing.T) {
	if testing.Short() {
		t.Skip("corre o gerador; -short salta")
	}
	cmd := exec.Command("go", "run", "./cmd/openapi")
	cmd.Dir = filepath.Join("..", "..", "..")
	var saida, erros bytes.Buffer
	cmd.Stdout, cmd.Stderr = &saida, &erros
	if err := cmd.Run(); err != nil {
		t.Fatalf("o gerador falhou: %v\n%s", err, erros.String())
	}

	noDisco, err := os.ReadFile(filepath.Join("..", "..", "..", "openapi.json"))
	if err != nil {
		t.Fatalf("sem openapi.json — corre `go run ./cmd/openapi > openapi.json`: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(noDisco), bytes.TrimSpace(saida.Bytes())) {
		t.Errorf("a especificação no disco não é a que o gerador faz agora.\n\n"+
			"Corre:  go run ./cmd/openapi > openapi.json\n\n%s", diferenca(noDisco, saida.Bytes()))
	}
}

/** As primeiras linhas que diferem. O ficheiro inteiro não cabe num erro. */
func diferenca(a, b []byte) string {
	linhasA, linhasB := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	var out []string
	for i := 0; i < len(linhasA) || i < len(linhasB); i++ {
		la, lb := "", ""
		if i < len(linhasA) {
			la = linhasA[i]
		}
		if i < len(linhasB) {
			lb = linhasB[i]
		}
		if la == lb {
			continue
		}
		out = append(out, fmt.Sprintf("linha %d:\n  no disco: %s\n  agora:    %s", i+1, la, lb))
		if len(out) == 6 {
			break
		}
	}
	return strings.Join(out, "\n")
}
