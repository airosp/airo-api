package middleware

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

/*
 * As métricas: quantos pedidos, com que respostas, e quão depressa.
 *
 * ⚠️ Os registos são estruturados e ninguém os agrega. Quando o `/v1/classes`
 * começou a devolver 500 em produção, só se soube porque alguém foi ver o
 * registo à mão — e só depois de reparar que a app não mostrava aulas. Sem
 * contadores não há como perguntar "isto está pior do que ontem?" sem ler um
 * milhão de linhas.
 *
 * Em texto simples e no formato que o Prometheus lê, que é o que quase tudo
 * lê hoje. Sem dependência nova: são três mapas e um `fmt.Fprintf` — trazer uma
 * biblioteca para contar inteiros era pagar um custo de manutenção por
 * conveniência.
 *
 * O que **não** se conta: nada por utilizador, nada por telefone, nada que
 * identifique alguém. Uma métrica é um número por rota, e é assim que se
 * mantém.
 */

// Escalões de latência, em milissegundos.
//
// Escolhidos à volta do que interessa: 800 ms é o limite do `/training/today`
// que os documentos pedem, por isso há escalões dos dois lados dele.
var escaloes = []float64{25, 50, 100, 200, 400, 800, 1600, 3200}

type contadores struct {
	mu sync.Mutex
	// pedidos[rota][status] = quantos.
	pedidos map[string]map[int]int64
	// histograma[rota][escalão] = quantos abaixo desse tempo (acumulado no fim).
	histograma map[string][]int64
	somaMs     map[string]float64
	totais     map[string]int64
	desde      time.Time
}

var registo = &contadores{
	pedidos:    map[string]map[int]int64{},
	histograma: map[string][]int64{},
	somaMs:     map[string]float64{},
	totais:     map[string]int64{},
	desde:      time.Now(),
}

/*
 * Metricas conta cada pedido.
 *
 * Entra **fora** da cadeia de autenticação: um 401 é tão interessante como um
 * 200 — mais, até, quando são muitos de repente.
 */
func Metricas(mux *http.ServeMux) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inicio := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			registar(rotaDe(mux, r), sw.status, float64(time.Since(inicio).Microseconds())/1000)
		})
	}
}

/*
 * rotaDe devolve o **padrão** da rota, e não o caminho.
 *
 * ⚠️ Sem isto, cada identificador na URL criava uma métrica nova: mil aulas
 * davam mil séries temporais, e o sistema que as guarda ficava sem memória
 * antes de alguém reparar. É o erro clássico, e tem nome: cardinalidade.
 *
 * O `http.ServeMux` do Go sabe o padrão que casou, e é o que se usa. Sem
 * padrão — um 404 — fica "outro", que é exactamente o que se quer saber.
 */
func rotaDe(mux *http.ServeMux, r *http.Request) string {
	if mux == nil {
		return "outro"
	}
	// Pergunta-se ao mux em vez de se ler `r.Pattern`: este middleware corre
	// **por fora** do mux, e o padrão é escrito no pedido que ele passa para
	// dentro — não neste. `Handler` só procura, não executa.
	_, padrao := mux.Handler(r)
	if padrao == "" {
		return "outro"
	}
	// O padrão vem como "GET /v1/classes/{id}". O método já vai à parte.
	if i := strings.IndexByte(padrao, ' '); i >= 0 {
		padrao = padrao[i+1:]
	}
	return padrao
}

func registar(rota string, status int, ms float64) {
	registo.mu.Lock()
	defer registo.mu.Unlock()

	if registo.pedidos[rota] == nil {
		registo.pedidos[rota] = map[int]int64{}
		registo.histograma[rota] = make([]int64, len(escaloes)+1)
	}
	registo.pedidos[rota][status]++
	registo.totais[rota]++
	registo.somaMs[rota] += ms

	i := sort.SearchFloat64s(escaloes, ms)
	registo.histograma[rota][i]++
}

/*
 * Metrics serve os contadores.
 *
 * Público e sem sessão: é uma rota de operação, e quem lhe chama é o
 * recolector, que não tem conta. Não devolve nada sobre ninguém — só números
 * por rota. Ainda assim fica atrás do que quer que o alojamento use para a
 * fechar ao exterior; aqui não se inventa autenticação nova para uma coisa que
 * a infraestrutura resolve melhor.
 */
func Metrics(w http.ResponseWriter, r *http.Request) {
	registo.mu.Lock()
	defer registo.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fmt.Fprintln(w, "# HELP airo_pedidos_total Pedidos servidos, por rota e resposta.")
	fmt.Fprintln(w, "# TYPE airo_pedidos_total counter")
	for _, rota := range ordenadas(registo.pedidos) {
		for status, quantos := range registo.pedidos[rota] {
			fmt.Fprintf(w, "airo_pedidos_total{rota=%q,status=\"%d\"} %d\n", rota, status, quantos)
		}
	}

	fmt.Fprintln(w, "# HELP airo_duracao_ms Duração dos pedidos, em milissegundos.")
	fmt.Fprintln(w, "# TYPE airo_duracao_ms histogram")
	for _, rota := range ordenadasHist(registo.histograma) {
		var acumulado int64
		for i, escalao := range escaloes {
			acumulado += registo.histograma[rota][i]
			fmt.Fprintf(w, "airo_duracao_ms_bucket{rota=%q,le=\"%g\"} %d\n", rota, escalao, acumulado)
		}
		acumulado += registo.histograma[rota][len(escaloes)]
		fmt.Fprintf(w, "airo_duracao_ms_bucket{rota=%q,le=\"+Inf\"} %d\n", rota, acumulado)
		fmt.Fprintf(w, "airo_duracao_ms_sum{rota=%q} %g\n", rota, registo.somaMs[rota])
		fmt.Fprintf(w, "airo_duracao_ms_count{rota=%q} %d\n", rota, registo.totais[rota])
	}

	fmt.Fprintln(w, "# HELP airo_desde_segundos Há quanto tempo este processo está de pé.")
	fmt.Fprintln(w, "# TYPE airo_desde_segundos gauge")
	fmt.Fprintf(w, "airo_desde_segundos %d\n", int64(time.Since(registo.desde).Seconds()))
}

func ordenadas(m map[string]map[int]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ordenadasHist(m map[string][]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Repor limpa os contadores. Só para ensaios: em produção um contador que se
// repõe sozinho é um contador que mente.
func Repor() {
	registo.mu.Lock()
	defer registo.mu.Unlock()
	registo.pedidos = map[string]map[int]int64{}
	registo.histograma = map[string][]int64{}
	registo.somaMs = map[string]float64{}
	registo.totais = map[string]int64{}
	registo.desde = time.Now()
}
