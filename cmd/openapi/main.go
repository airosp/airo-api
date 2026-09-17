// openapi escreve a especificação a partir do **código que corre**.
//
// ⚠️ Os tipos do cliente são escritos à mão ao lado dos do servidor. Custou um
// `422` numa ronda: o cliente mandava `targetKg` onde o servidor lê
// `targetWeightKg`, e nada o apanhou antes do browser. Cada campo novo é
// escrito duas vezes, em duas línguas, e nada os confronta.
//
// Isto não escreve uma terceira cópia à mão. Lê as rotas do router — as mesmas
// que servem os pedidos — e as formas das respostas dos golden files, que são
// geradas das respostas verdadeiras. Uma rota que se acrescente aparece aqui
// sem ninguém fazer nada; um campo que mude de nome aparece como mudança.
//
//	go run ./cmd/openapi > openapi.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// rota é uma linha do inventário.
type rota struct {
	Metodo   string
	Caminho  string
	Privada  bool
	Resposta string // o nome do golden file, quando existe
}

/*
 * As rotas lêem-se do **router**, não de uma lista ao lado dele.
 *
 * ⚠️ A primeira versão disto tinha a lista escrita à mão, e nasceu já
 * desactualizada: faltavam-lhe sete rotas que o cliente chamava todos os dias.
 * Uma lista à mão ao lado do código é uma terceira cópia — exactamente o
 * problema que esta especificação existe para resolver.
 *
 * Lê-se o ficheiro em vez de se perguntar ao `http.ServeMux` porque ele não
 * enumera os padrões que conhece. O ficheiro é a fonte, e um `mux.Handle` novo
 * aparece aqui sem ninguém fazer nada.
 */
func lerRotas() ([]rota, error) {
	bruto, err := os.ReadFile(filepath.Join("internal", "transport", "http", "router.go"))
	if err != nil {
		return nil, err
	}

	// Privadas: as que passam por `middleware.Auth`. Na prática é tudo o que
	// está dentro do bloco das rotas privadas, e é o bloco que se lê.
	linhas := strings.Split(string(bruto), "\n")
	padrao := regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/[^"]*)"`)

	vistas := map[string]bool{}
	var out []rota
	for i, linha := range linhas {
		m := padrao.FindStringSubmatch(linha)
		if m == nil {
			continue
		}
		chave := m[1] + " " + m[2]
		if vistas[chave] {
			continue
		}
		vistas[chave] = true

		// A autenticação está na mesma linha ou na seguinte, conforme a
		// chamada tenha sido partida para caber.
		contexto := linha
		if i+1 < len(linhas) {
			contexto += linhas[i+1]
		}
		out = append(out, rota{
			Metodo:   m[1],
			Caminho:  m[2],
			Privada:  strings.Contains(contexto, "middleware.Auth") || strings.Contains(contexto, "private("),
			Resposta: golden[m[1]+" "+m[2]],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Caminho != out[j].Caminho {
			return out[i].Caminho < out[j].Caminho
		}
		return out[i].Metodo < out[j].Metodo
	})
	return out, nil
}

/*
 * Que golden file descreve a resposta de cada rota.
 *
 * Isto **é** escrito à mão, e é a única parte que tem de ser: é a ligação entre
 * uma rota e o ficheiro que guarda a forma da resposta dela. Uma rota sem
 * entrada aqui entra na especificação sem esquema, e diz-se que assim é.
 */
var golden = map[string]string{
	"GET /v1/training/today":                 "training-today",
	"GET /v1/profile":                        "profile",
	"PUT /v1/profile":                        "profile",
	"GET /v1/auth/sessions":                  "auth-sessions",
	"GET /v1/nutrition/shopping-list/{week}": "shopping-list",
	"PUT /v1/nutrition/shopping-list/{week}": "shopping-list",
}

func main() {
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "Airo API",
			"description": "Gerado por `go run ./cmd/openapi`. " +
				"As formas das respostas vêm dos golden files, que por sua vez vêm " +
				"de respostas verdadeiras — não há terceira cópia escrita à mão.",
			"version": "1",
		},
		"servers": []any{
			map[string]any{"url": "https://airo-api.savanapoint.com"},
			map[string]any{"url": "http://localhost:8090", "description": "ensaio local"},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"sessao": map[string]any{
					"type": "http", "scheme": "bearer", "bearerFormat": "JWT",
				},
			},
		},
	}

	rotas, err := lerRotas()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ler as rotas:", err)
		os.Exit(1)
	}

	paths := map[string]any{}
	for _, r := range rotas {
		item, _ := paths[r.Caminho].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[r.Caminho] = item
		}

		op := map[string]any{
			"operationId": operacao(r),
			"responses":   respostas(r),
		}
		if r.Privada {
			op["security"] = []any{map[string]any{"sessao": []any{}}}
		}
		if params := parametros(r.Caminho); len(params) > 0 {
			op["parameters"] = params
		}
		item[strings.ToLower(r.Metodo)] = op
	}
	doc["paths"] = paths

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// operacao dá um nome estável a cada rota, para o cliente gerado ter funções
// com nome em vez de caminhos soltos.
func operacao(r rota) string {
	partes := strings.FieldsFunc(r.Caminho, func(c rune) bool {
		return c == '/' || c == '{' || c == '}' || c == '-'
	})
	var sb strings.Builder
	sb.WriteString(strings.ToLower(r.Metodo))
	for _, p := range partes {
		if p == "v1" {
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return sb.String()
}

func parametros(caminho string) []any {
	var out []any
	for _, p := range strings.Split(caminho, "/") {
		if !strings.HasPrefix(p, "{") {
			continue
		}
		nome := strings.Trim(p, "{}")
		out = append(out, map[string]any{
			"name": nome, "in": "path", "required": true,
			"schema": map[string]any{"type": "string"},
		})
	}
	return out
}

/*
 * respostas lê a forma do golden file, quando existe.
 *
 * É esta a parte que faz a especificação valer: os campos não são escritos aqui
 * — vêm da resposta que o servidor deu mesmo, gravada por
 * `go test -run Golden -actualizar`.
 */
func respostas(r rota) map[string]any {
	ok := map[string]any{"description": "Feito."}
	if r.Resposta != "" {
		if esquema := esquemaDoGolden(r.Resposta); esquema != nil {
			ok["content"] = map[string]any{
				"application/json": map[string]any{"schema": esquema},
			}
		}
	}
	out := map[string]any{"200": ok}
	if r.Privada {
		out["401"] = map[string]any{"description": "Sessão inválida ou expirada."}
	}
	return out
}

func esquemaDoGolden(nome string) map[string]any {
	bruto, err := os.ReadFile(filepath.Join("internal", "transport", "http", "testdata", "golden", nome+".json"))
	if err != nil {
		return nil
	}
	var forma map[string]string
	if err := json.Unmarshal(bruto, &forma); err != nil {
		return nil
	}

	propriedades := map[string]any{}
	caminhos := make([]string, 0, len(forma))
	for k := range forma {
		caminhos = append(caminhos, k)
	}
	sort.Strings(caminhos)

	for _, caminho := range caminhos {
		// Só o primeiro nível: o aninhamento completo dava um gerador de
		// esquemas, e o que aqui se quer é o contrato dos nomes.
		topo := caminho
		if i := strings.IndexAny(topo, ".["); i > 0 {
			topo = topo[:i]
		}
		if topo == "" {
			continue
		}
		if _, existe := propriedades[topo]; existe {
			continue
		}
		propriedades[topo] = map[string]any{"type": tipoJSON(forma[caminho], caminho)}
	}
	return map[string]any{"type": "object", "properties": propriedades}
}

func tipoJSON(tipo, caminho string) string {
	if strings.Contains(caminho, "[]") {
		return "array"
	}
	if strings.Contains(caminho, ".") {
		return "object"
	}
	switch tipo {
	case "texto":
		return "string"
	case "número":
		return "number"
	case "booleano":
		return "boolean"
	default:
		return "object"
	}
}
