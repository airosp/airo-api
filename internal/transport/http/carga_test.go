package http_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

/*
 * A carga do treino de hoje.
 *
 * `T6.9` pede o p95 abaixo de 800 ms. O número não é arbitrário: é o ecrã que
 * a pessoa abre para começar a treinar, e um segundo de espera antes de um
 * treino é o tempo que chega para desistir.
 *
 * ⚠️ Isto mede o **servidor**, não a rede de Moçambique. Uma resposta em 40 ms
 * daqui pode ser um segundo e meio num telemóvel com 3G numa vila — o que este
 * teste garante é que o tempo que gastamos é nosso e é pequeno. O resto é
 * tamanho de resposta e é outra conversa.
 *
 * Não corre por omissão: sessenta pedidos concorrentes contra um postgres
 * embutido demoram, e um teste lento que corre a cada gravação acaba
 * desligado. Corre assim:
 *
 *	AIRO_CARGA=1 go test ./internal/transport/http/ -run Carga -v
 */
func TestCargaDoTreinoDeHoje(t *testing.T) {
	if os.Getenv("AIRO_CARGA") == "" {
		t.Skip("prova de carga: AIRO_CARGA=1 para correr")
	}

	h, _, _ := serveTraining(t)

	// Uma volta a frio antes de medir: a primeira resposta paga o arranque das
	// ligações e do catálogo, e medi-la era medir o arranque.
	if w := get(t, h, "/v1/training/today?localDay=2026-09-12"); w.Code != http.StatusOK {
		t.Fatalf("aquecimento: %d — %s", w.Code, w.Body.String())
	}

	const (
		// Doze ao mesmo tempo: mais do que uma máquina de produção vê num
		// segundo com as primeiras centenas de pessoas, e o suficiente para a
		// contenção aparecer se existir.
		emParalelo = 12
		porCadaUm  = 20
	)

	tempos := make([]time.Duration, 0, emParalelo*porCadaUm)
	var mu sync.Mutex
	var erros int

	var wg sync.WaitGroup
	for i := 0; i < emParalelo; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < porCadaUm; j++ {
				// Dias diferentes por cliente: o mesmo dia para todos deixava
				// qualquer cache esconder o trabalho real.
				dia := fmt.Sprintf("2026-09-%02d", 1+(n+j)%28)
				r := httptest.NewRequest(http.MethodGet, "/v1/training/today?localDay="+dia, nil)
				r.Header.Set("Authorization", "Bearer token-de-teste")
				w := httptest.NewRecorder()

				inicio := time.Now()
				h.ServeHTTP(w, r)
				gasto := time.Since(inicio)

				mu.Lock()
				tempos = append(tempos, gasto)
				if w.Code != http.StatusOK {
					erros++
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if erros > 0 {
		t.Fatalf("%d de %d pedidos falharam", erros, len(tempos))
	}
	sort.Slice(tempos, func(i, j int) bool { return tempos[i] < tempos[j] })

	p := func(q float64) time.Duration {
		i := int(float64(len(tempos))*q) - 1
		if i < 0 {
			i = 0
		}
		return tempos[i]
	}
	p50, p95, p99 := p(0.50), p(0.95), p(0.99)
	t.Logf("%d pedidos · p50 %v · p95 %v · p99 %v · pior %v",
		len(tempos), p50.Round(time.Millisecond), p95.Round(time.Millisecond),
		p99.Round(time.Millisecond), tempos[len(tempos)-1].Round(time.Millisecond))

	if p95 > 800*time.Millisecond {
		t.Errorf("p95 %v — acima dos 800 ms que a T6.9 pede", p95.Round(time.Millisecond))
	}
}
