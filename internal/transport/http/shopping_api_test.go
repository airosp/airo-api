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

func serveCompras(t *testing.T) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth:        fakeAuth{userID: userID},
		Shopping:    &handlers.Shopping{Store: repo.NewShoppingRepo(repo.NewTxManager(pool))},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

type listaJSON struct {
	Week    string   `json:"week"`
	Checked []string `json:"checked"`
	Extras  []struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Note     string `json:"note"`
		Category string `json:"category"`
		Grams    int    `json:"grams"`
	} `json:"extras"`
	Prices map[string]float64 `json:"prices"`
}

func lista(t *testing.T, h http.Handler, semana string) listaJSON {
	t.Helper()
	w := get(t, h, "/v1/nutrition/shopping-list/"+semana)
	if w.Code != http.StatusOK {
		t.Fatalf("ler lista: %d — %s", w.Code, w.Body.String())
	}
	var body listaJSON
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta ilegível: %v — %s", err, w.Body.String())
	}
	return body
}

// 2026-09-16 é uma quarta-feira; a segunda dessa semana é 14.
const quarta = "2026-09-16"
const segunda = "2026-09-14"

/*
 * O que foi riscado e o que foi acrescentado à mão sobrevive.
 *
 * Era isto que não existia: o plano dizia o que comer todos os dias e ninguém o
 * conseguia levar para o mercado.
 */
func TestAListaGuardaOQueAPessoaDecidiu(t *testing.T) {
	h := serveCompras(t)

	corpo := `{"checked":["chicken","rice"],
	           "extras":[{"id":"e1","label":"Sabão","note":"o azul"}]}`
	if w := put(t, h, "/v1/nutrition/shopping-list/"+quarta, corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	l := lista(t, h, quarta)
	if len(l.Checked) != 2 || l.Checked[0] != "chicken" {
		t.Errorf("riscados: %v", l.Checked)
	}
	if len(l.Extras) != 1 || l.Extras[0].Label != "Sabão" || l.Extras[0].Note != "o azul" {
		t.Errorf("extras: %+v", l.Extras)
	}
}

/*
 * Qualquer dia da semana dá a mesma lista.
 *
 * Dois aparelhos com ideias diferentes sobre onde começa a semana davam duas
 * listas para a mesma semana, e a pessoa via metade do que tinha riscado. A
 * semana normaliza-se no servidor, à segunda.
 */
func TestQualquerDiaDaSemanaDaAMesmaLista(t *testing.T) {
	h := serveCompras(t)

	if w := put(t, h, "/v1/nutrition/shopping-list/"+quarta,
		`{"checked":["chicken"]}`); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	// Segunda, sexta e domingo da mesma semana.
	for _, dia := range []string{segunda, "2026-09-18", "2026-09-20"} {
		l := lista(t, h, dia)
		if l.Week != segunda {
			t.Errorf("%s caiu na semana %s", dia, l.Week)
		}
		if len(l.Checked) != 1 || l.Checked[0] != "chicken" {
			t.Errorf("%s: riscados %v", dia, l.Checked)
		}
	}

	// A semana seguinte é outra lista, e começa limpa.
	if l := lista(t, h, "2026-09-21"); len(l.Checked) != 0 {
		t.Errorf("a semana seguinte veio com %v", l.Checked)
	}
}

// Uma semana por tocar é uma lista por riscar, e não um erro.
func TestSemanaPorTocarVemVazia(t *testing.T) {
	h := serveCompras(t)
	l := lista(t, h, quarta)
	if l.Week != segunda || len(l.Checked) != 0 || len(l.Extras) != 0 {
		t.Fatalf("veio %+v", l)
	}
}

/*
 * Riscar duas vezes o mesmo é riscá-lo uma vez.
 *
 * E um item sem nome não entra: tocar em "acrescentar" sem escrever nada
 * deixava uma linha em branco na lista para sempre.
 */
func TestListaLimpaOQueNaoFazSentido(t *testing.T) {
	h := serveCompras(t)

	corpo := `{"checked":["rice","rice","  ",""],
	           "extras":[{"id":"e1","label":"  "},{"id":"","label":"Pão"},{"id":"e2","label":" Pão "}]}`
	if w := put(t, h, "/v1/nutrition/shopping-list/"+quarta, corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	l := lista(t, h, quarta)
	if len(l.Checked) != 1 || l.Checked[0] != "rice" {
		t.Errorf("riscados: %v", l.Checked)
	}
	if len(l.Extras) != 1 || l.Extras[0].Label != "Pão" {
		t.Errorf("extras: %+v", l.Extras)
	}
}

// As semanas passadas vão fora à boleia da escrita seguinte.
func TestSemanasVelhasVaoFora(t *testing.T) {
	h := serveCompras(t)

	velha := "2026-08-03" // cinco semanas antes
	if w := put(t, h, "/v1/nutrition/shopping-list/"+velha,
		`{"checked":["couve"]}`); w.Code != http.StatusOK {
		t.Fatalf("gravar velha: %d — %s", w.Code, w.Body.String())
	}
	if l := lista(t, h, velha); len(l.Checked) != 1 {
		t.Fatalf("a velha não ficou gravada: %+v", l)
	}

	if w := put(t, h, "/v1/nutrition/shopping-list/"+quarta,
		`{"checked":["chicken"]}`); w.Code != http.StatusOK {
		t.Fatalf("gravar desta semana: %d — %s", w.Code, w.Body.String())
	}

	if l := lista(t, h, velha); len(l.Checked) != 0 {
		t.Errorf("a lista de há cinco semanas continua lá: %v", l.Checked)
	}
	if l := lista(t, h, quarta); len(l.Checked) != 1 {
		t.Errorf("levou a desta semana à frente: %v", l.Checked)
	}
}

// Uma semana que não é uma semana não escreve nada.
func TestSemanaInvalidaRecusada(t *testing.T) {
	h := serveCompras(t)
	for _, s := range []string{"semana-passada", "2026-13-40"} {
		if w := put(t, h, "/v1/nutrition/shopping-list/"+s,
			`{"checked":["rice"]}`); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q devolveu %d — %s", s, w.Code, w.Body.String())
		}
	}
}

/*
 * Um item escrito à mão entra no grupo que a pessoa escolheu, e com peso.
 *
 * ⚠️ Tudo o que era escrito à mão caía num saco no fim chamado "mais alguma
 * coisa": quem quisesse acrescentar manga ia à fruta e não a encontrava lá.
 * Quem anda pelo mercado anda por secções, e uma manga longe da fruta é uma
 * manga que se esquece.
 */
func TestUmExtraEntraNoGrupoEscolhido(t *testing.T) {
	h := serveCompras(t)

	corpo := `{"extras":[
	   {"id":"e1","label":"Manga","category":"fruit","grams":1500},
	   {"id":"e2","label":"Sabão"},
	   {"id":"e3","label":"Cuscuz","category":"inventado"}]}`
	if w := put(t, h, "/v1/nutrition/shopping-list/"+quarta, corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	l := lista(t, h, quarta)
	if len(l.Extras) != 3 {
		t.Fatalf("extras: %+v", l.Extras)
	}
	if l.Extras[0].Category != "fruit" || l.Extras[0].Grams != 1500 {
		t.Errorf("a manga não ficou na fruta: %+v", l.Extras[0])
	}
	// Sem grupo e com um grupo que não existe caem os dois no mesmo sítio: é
	// melhor aparecerem no fim do que desaparecerem.
	if l.Extras[1].Category != "outros" || l.Extras[2].Category != "outros" {
		t.Errorf("caíram fora de outros: %+v", l.Extras[1:])
	}
}

/*
 * Os preços sobrevivem, e o que não é um preço não entra.
 *
 * Uma lista de compras sem preços é uma lista de intenções. O preço não vem do
 * plano — muda de mercado para mercado —, por isso é a pessoa que o aponta.
 */
func TestOsPrecosFicamGravados(t *testing.T) {
	h := serveCompras(t)

	corpo := `{"checked":["chicken"],
	           "prices":{"chicken":450.5,"rice":0,"couve":-12,"lixo":1000001,"e1":12.349}}`
	if w := put(t, h, "/v1/nutrition/shopping-list/"+quarta, corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	l := lista(t, h, quarta)
	if l.Prices["chicken"] != 450.5 {
		t.Errorf("o preço do frango: %v", l.Prices["chicken"])
	}
	// Duas casas, que é o que o metical tem. Mais do que isso é ruído.
	if l.Prices["e1"] != 12.35 {
		t.Errorf("as casas decimais: %v", l.Prices["e1"])
	}
	for _, chave := range []string{"rice", "couve", "lixo"} {
		if _, existe := l.Prices[chave]; existe {
			t.Errorf("%q entrou com %v", chave, l.Prices[chave])
		}
	}
}

// Uma semana por tocar traz os preços vazios, e não `null`.
func TestSemPrecosVemMapaVazio(t *testing.T) {
	h := serveCompras(t)
	if l := lista(t, h, quarta); l.Prices == nil {
		t.Fatal("prices veio nulo — o cliente tem de distinguir vazio de ausente")
	}
}
