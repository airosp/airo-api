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
	Metodo  string
	Caminho string
	Privada bool
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
			Metodo:  m[1],
			Caminho: m[2],
			Privada: strings.Contains(contexto, "middleware.Auth") || strings.Contains(contexto, "private("),
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
 * O contrato gravado pela suite de testes.
 *
 * ⚠️ A ligação rota → forma era escrita à mão, e cobria seis rotas de
 * sessenta e quatro. O resto da API ficava sem esquema nenhum — e escrever
 * quarenta e três testes à mão para lá chegar era um dia de trabalho a
 * produzir uma cobertura que envelhecia à primeira rota nova.
 *
 * Agora vem do `testdata/contrato.json`, que os testes que já existem gravam
 * ao correr (ver `gravador_test.go`). Um teste novo traz cobertura de contrato
 * sem ninguém fazer nada, e uma rota sem teste **não aparece** — que é a
 * verdade, e não um número a fingir.
 */
type chamadaGravada struct {
	Metodo   string            `json:"metodo"`
	Caminho  string            `json:"caminho"`
	Status   int               `json:"status"`
	Pedido   map[string]string `json:"pedido"`
	Resposta map[string]string `json:"resposta"`
}

/*
 * lerContrato devolve o que a suite gravou, por "MÉTODO padrão".
 *
 * Os caminhos gravados são concretos — `/v1/classes/aula_x` — e as rotas são
 * padrões. Casam-se aqui, com a lista que já se leu do router: é o mesmo
 * conhecimento a servir duas vezes em vez de ser escrito duas vezes.
 */
func lerContrato(rotas []rota) map[string]chamadaGravada {
	out := map[string]chamadaGravada{}
	bruto, err := os.ReadFile(filepath.Join("internal", "transport", "http", "testdata", "contrato.json"))
	if err != nil {
		return out
	}
	var lista []chamadaGravada
	if err := json.Unmarshal(bruto, &lista); err != nil {
		return out
	}

	padroes := make(map[string]*regexp.Regexp, len(rotas))
	for _, r := range rotas {
		padroes[r.Metodo+" "+r.Caminho] = comoRegexp(r.Caminho)
	}

	for _, c := range lista {
		if c.Status >= 400 {
			// Uma recusa descreve o que o servidor **não** aceita.
			continue
		}
		for chave, re := range padroes {
			if !strings.HasPrefix(chave, c.Metodo+" ") {
				continue
			}
			if !re.MatchString(c.Caminho) {
				continue
			}
			// Fica a que descreve mais campos.
			if velha, existe := out[chave]; existe &&
				len(velha.Pedido)+len(velha.Resposta) >= len(c.Pedido)+len(c.Resposta) {
				continue
			}
			out[chave] = c
		}
	}
	return out
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

	contrato := lerContrato(rotas)
	semContrato := 0

	paths := map[string]any{}
	for _, r := range rotas {
		item, _ := paths[r.Caminho].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[r.Caminho] = item
		}

		gravada, temContrato := contrato[r.Metodo+" "+r.Caminho]

		op := map[string]any{
			"operationId": operacao(r),
			"responses":   respostas(r, gravada),
		}
		if temContrato && len(gravada.Pedido) > 0 {
			op["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{"schema": esquemaDe(gravada.Pedido)},
				},
			}
		}
		if !temContrato {
			semContrato++
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

	/*
	 * A cobertura vai para o erro padrão, e não para o ficheiro.
	 *
	 * É um número que interessa a quem corre o comando e não ao documento: uma
	 * rota sem contrato é uma rota sem teste, e dizê-lo alto é o que faz a
	 * cobertura subir. Calar era deixar a especificação parecer completa.
	 */
	fmt.Fprintf(os.Stderr, "%d rotas · %d com contrato gravado · %d sem nenhum teste a tocá-las\n",
		len(rotas), len(rotas)-semContrato, semContrato)
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
func respostas(r rota, gravada chamadaGravada) map[string]any {
	ok := map[string]any{"description": "Feito."}
	if len(gravada.Resposta) > 0 {
		ok["content"] = map[string]any{
			"application/json": map[string]any{"schema": esquemaDe(gravada.Resposta)},
		}
	}
	codigo := "200"
	if gravada.Status >= 200 && gravada.Status < 300 {
		codigo = fmt.Sprint(gravada.Status)
	}
	out := map[string]any{codigo: ok}
	if r.Privada {
		out["401"] = map[string]any{"description": "Sessão inválida ou expirada."}
	}
	return out
}

/*
 * esquemaDe traduz a forma gravada — caminho de campo → tipo — para um esquema.
 *
 * Só o primeiro nível fica como propriedade: o aninhamento completo dava um
 * gerador de esquemas, e o que aqui se quer é o **contrato dos nomes**. É o
 * nome que diverge entre os dois lados, não a profundidade.
 */
func esquemaDe(forma map[string]string) map[string]any {
	propriedades := map[string]any{}
	caminhos := make([]string, 0, len(forma))
	for k := range forma {
		caminhos = append(caminhos, k)
	}
	sort.Strings(caminhos)

	for _, caminho := range caminhos {
		topo := caminho
		if i := strings.IndexAny(topo, ".["); i > 0 {
			topo = topo[:i]
		}
		if topo == "" || propriedades[topo] != nil {
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

/*
 * comoRegexp traduz um padrão de rota para uma expressão que casa caminhos.
 *
 * `/v1/classes/{id}` passa a casar `/v1/classes/aula_x`. Constrói-se segmento a
 * segmento em vez de escapar o padrão inteiro e desfazer o escape a seguir —
 * foi o que tentei primeiro, e o resultado casou dezoito rotas de setenta e
 * seis sem dizer que estava errado. Uma cobertura que falha em silêncio é pior
 * do que nenhuma, porque parece boa.
 */
func comoRegexp(padrao string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	for i, segmento := range strings.Split(padrao, "/") {
		if i > 0 {
			sb.WriteString("/")
		}
		if strings.HasPrefix(segmento, "{") && strings.HasSuffix(segmento, "}") {
			sb.WriteString("[^/]+")
			continue
		}
		sb.WriteString(regexp.QuoteMeta(segmento))
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}
