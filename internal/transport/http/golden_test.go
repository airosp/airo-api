package http_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

/*
 * Golden files: a **forma** das respostas, fixada.
 *
 * ⚠️ Os testes que já existem verificam o que uma resposta diz — que o peso é
 * 74, que o estado é "completed". Nenhum verifica a **forma**: um campo que
 * mude de nome, desapareça ou passe de número para texto passa por todos eles e
 * parte a app, porque os tipos do cliente são escritos à mão do outro lado.
 *
 * Isto grava a forma num ficheiro e compara-a a cada corrida. Não é o mesmo que
 * o contrato gerado do OpenAPI — é mais barato e apanha o mesmo: se mudar,
 * alguém tem de dizer que sim.
 *
 * Grava-se a **forma** e não os valores: um identificador gerado ou uma data de
 * hoje mudam a cada corrida e não dizem nada sobre o contrato. O que fica é o
 * caminho de cada campo e o tipo do que lá está.
 *
 *	go test ./internal/transport/http/ -run Golden -actualizar
 *
 * …reescreve os ficheiros. Sem a bandeira, uma diferença é um erro.
 */

var actualizar = flag.Bool("actualizar", false, "reescrever os golden files")

// forma reduz um JSON ao seu esqueleto: caminho de campo → tipo.
//
// As listas colapsam no primeiro elemento: uma lista de dez aulas tem a forma
// de uma aula, e guardar as dez era guardar dados a fingir que era contrato.
func forma(v any, prefixo string, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		/*
		 * Um objecto vazio deixa rasto, como uma lista vazia.
		 *
		 * ⚠️ Sem isto ele **desaparecia** da forma: o `prices` da lista de
		 * compras chega vazio numa semana por tocar, não gravava nada, e o
		 * contrato ficava a dizer que aquele campo não existe. Um campo que o
		 * contrato não conhece é um campo que se pode apagar do servidor sem
		 * ninguém dar por isso — que é exactamente o que isto existe para
		 * apanhar.
		 */
		if len(t) == 0 && prefixo != "" {
			out[prefixo+"{}"] = "vazio"
			return
		}
		for k, val := range t {
			caminho := k
			if prefixo != "" {
				caminho = prefixo + "." + k
			}
			forma(val, caminho, out)
		}
	case []any:
		if len(t) == 0 {
			out[prefixo+"[]"] = "vazio"
			return
		}
		forma(t[0], prefixo+"[]", out)
	case string:
		out[prefixo] = "texto"
	case float64:
		out[prefixo] = "número"
	case bool:
		out[prefixo] = "booleano"
	case nil:
		out[prefixo] = "nulo"
	default:
		out[prefixo] = "?"
	}
}

func compararComGolden(t *testing.T, nome string, corpo []byte) {
	t.Helper()

	var v any
	if err := json.Unmarshal(corpo, &v); err != nil {
		t.Fatalf("%s: resposta não é JSON: %v", nome, err)
	}
	esqueleto := map[string]string{}
	forma(v, "", esqueleto)

	novo, err := json.MarshalIndent(esqueleto, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	novo = append(novo, '\n')

	caminho := filepath.Join("testdata", "golden", nome+".json")
	if *actualizar {
		if err := os.WriteFile(caminho, novo, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden actualizado: %s", caminho)
		return
	}

	velho, err := os.ReadFile(caminho)
	if os.IsNotExist(err) {
		t.Fatalf("sem golden para %s — corre com -actualizar para o criar:\n%s", nome, novo)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(velho), bytes.TrimSpace(novo)) {
		t.Errorf("a forma de %s mudou.\n\nestava:\n%s\nestá:\n%s\n\n"+
			"Se a mudança é intencional, corre com -actualizar **e** actualiza os tipos do cliente.",
			nome, velho, novo)
	}
}

func TestGoldenDoTreinoDeHoje(t *testing.T) {
	h, _, _ := serveTraining(t)
	w := get(t, h, "/v1/training/today?localDay=2026-09-12")
	if w.Code != http.StatusOK {
		t.Fatalf("%d — %s", w.Code, w.Body.String())
	}
	compararComGolden(t, "training-today", w.Body.Bytes())
}

func TestGoldenDoPerfil(t *testing.T) {
	h, _ := serveProfile(t)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}
	w := get(t, h, "/v1/profile")
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	compararComGolden(t, "profile", w.Body.Bytes())
}

func TestGoldenDasCompras(t *testing.T) {
	h := serveCompras(t)
	// Um extra **completo**, com grupo e peso, e um preço: os campos opcionais
	// ausentes não deixam forma nenhuma, e um contrato que não os conhece é um
	// contrato que os deixa apagar sem ninguém dar por isso.
	if w := put(t, h, "/v1/nutrition/shopping-list/2026-09-16",
		`{"checked":["rice"],
		  "extras":[{"id":"e1","label":"Sabão","category":"outros","grams":1500}],
		  "prices":{"rice":120.5}}`); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d", w.Code)
	}
	w := get(t, h, "/v1/nutrition/shopping-list/2026-09-16")
	compararComGolden(t, "shopping-list", w.Body.Bytes())
}

func TestGoldenDasSessoesDeAutenticacao(t *testing.T) {
	h, _ := serveSessoes(t)
	w := get(t, h, "/v1/auth/sessions")
	if w.Code != http.StatusOK {
		t.Fatalf("%d — %s", w.Code, w.Body.String())
	}
	compararComGolden(t, "auth-sessions", w.Body.Bytes())
}

func TestGoldenDoErro(t *testing.T) {
	h, _ := serveProfile(t)
	w := put(t, h, "/v1/profile", `{"displayName":"","heightCm":1}`)
	if w.Code == http.StatusOK {
		t.Fatal("esperava recusa")
	}
	// A forma de um erro é contrato tanto como a de um sucesso: é ela que o
	// cliente lê para decidir o que mostrar e em que campo.
	compararComGolden(t, "erro-validacao", w.Body.Bytes())
}
