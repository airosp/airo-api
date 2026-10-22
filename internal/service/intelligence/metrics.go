package intelligence

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

/*
 * Os números da inteligência, para o mesmo `/metrics` do resto da API.
 *
 * Três perguntas, três famílias: **funciona** (chamadas por resultado,
 * latência), **acerta** (propostas, itens desconhecidos, correcções da pessoa)
 * e **compensa** (tokens e custo). Contadores e mais nada — nenhum rótulo leva
 * quem pediu nem o que comeu.
 */
type counters struct {
	mu sync.Mutex
	// jobs[tarefa][estado]
	jobs map[string]map[string]int64
	// calls[tarefa][resultado]
	calls               map[string]map[string]int64
	latencyMs           map[string]float64
	inTokens, outTokens map[string]int64
	costMicro           map[string]int64
	appliedTotal        map[string]int64
	appliedEdited       map[string]int64
	dismissedTotal      map[string]int64
	itemsRecognized     int64
	itemsUnknown        int64
}

var metrics = &counters{
	jobs: map[string]map[string]int64{}, calls: map[string]map[string]int64{},
	latencyMs: map[string]float64{}, inTokens: map[string]int64{}, outTokens: map[string]int64{},
	costMicro: map[string]int64{}, appliedTotal: map[string]int64{}, appliedEdited: map[string]int64{},
	dismissedTotal: map[string]int64{},
}

func inc2(m map[string]map[string]int64, a, b string) {
	if m[a] == nil {
		m[a] = map[string]int64{}
	}
	m[a][b]++
}

func (c *counters) job(task, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inc2(c.jobs, task, status)
}

func (c *counters) call(task, outcome string, latency time.Duration, in, out, cost int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inc2(c.calls, task, outcome)
	c.latencyMs[task] += float64(latency.Milliseconds())
	c.inTokens[task] += in
	c.outTokens[task] += out
	c.costMicro[task] += cost
}

func (c *counters) items(recognized, unknown int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.itemsRecognized += int64(recognized)
	c.itemsUnknown += int64(unknown)
}

func (c *counters) applied(task string, edited bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appliedTotal[task]++
	if edited {
		c.appliedEdited[task]++
	}
}

func (c *counters) dismissed(task string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dismissedTotal[task]++
}

// WriteMetrics escreve os contadores no formato de texto do Prometheus.
func WriteMetrics(w io.Writer) {
	c := metrics
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Fprintln(w, "# HELP airo_ia_execucoes_total Execuções de inteligência, por tarefa e estado.")
	fmt.Fprintln(w, "# TYPE airo_ia_execucoes_total counter")
	for _, t := range keys2(c.jobs) {
		for _, st := range keys(c.jobs[t]) {
			fmt.Fprintf(w, "airo_ia_execucoes_total{tarefa=%q,estado=%q} %d\n", t, st, c.jobs[t][st])
		}
	}
	fmt.Fprintln(w, "# HELP airo_ia_chamadas_total Chamadas ao fornecedor, por tarefa e resultado.")
	fmt.Fprintln(w, "# TYPE airo_ia_chamadas_total counter")
	for _, t := range keys2(c.calls) {
		for _, o := range keys(c.calls[t]) {
			fmt.Fprintf(w, "airo_ia_chamadas_total{tarefa=%q,resultado=%q} %d\n", t, o, c.calls[t][o])
		}
	}
	for _, m := range []struct {
		name, help string
		data       map[string]int64
	}{
		{"airo_ia_tokens_entrada_total", "Tokens de entrada consumidos.", c.inTokens},
		{"airo_ia_tokens_saida_total", "Tokens de saída consumidos.", c.outTokens},
		{"airo_ia_custo_micro_usd_total", "Custo, em milionésimos de dólar.", c.costMicro},
		{"airo_ia_aplicadas_total", "Propostas confirmadas pela pessoa.", c.appliedTotal},
		{"airo_ia_aplicadas_corrigidas_total", "Propostas que a pessoa corrigiu antes de confirmar.", c.appliedEdited},
		{"airo_ia_dispensadas_total", "Propostas dispensadas.", c.dismissedTotal},
	} {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", m.name, m.help, m.name)
		for _, t := range keys(m.data) {
			fmt.Fprintf(w, "%s{tarefa=%q} %d\n", m.name, t, m.data[t])
		}
	}
	fmt.Fprintln(w, "# HELP airo_ia_latencia_ms_soma Soma da latência das chamadas, em milissegundos.")
	fmt.Fprintln(w, "# TYPE airo_ia_latencia_ms_soma counter")
	for _, t := range keysF(c.latencyMs) {
		fmt.Fprintf(w, "airo_ia_latencia_ms_soma{tarefa=%q} %g\n", t, c.latencyMs[t])
	}
	fmt.Fprintln(w, "# HELP airo_ia_itens_total Itens nas propostas, reconhecidos e desconhecidos.")
	fmt.Fprintln(w, "# TYPE airo_ia_itens_total counter")
	fmt.Fprintf(w, "airo_ia_itens_total{tipo=\"reconhecido\"} %d\n", c.itemsRecognized)
	fmt.Fprintf(w, "airo_ia_itens_total{tipo=\"desconhecido\"} %d\n", c.itemsUnknown)
}

func keys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keys2(m map[string]map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysF(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
