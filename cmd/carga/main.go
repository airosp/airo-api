//go:build devtools

// carga mede o tempo de resposta de um endpoint sob concorrência.
//
// O p95 é o que interessa, não a média: uma média de 200 ms com 5% dos pedidos
// a dois segundos é uma app que parece lenta a uma pessoa em cada vinte — e
// essa pessoa não sabe que teve azar.
//
//	go run -tags devtools ./cmd/carga <url> <token>
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

// Mede o tempo de resposta sob concorrência.
//
// O p95 é o que interessa e não a média: uma média de 200 ms com 5% dos
// pedidos a dois segundos é uma app que parece lenta a uma pessoa em cada
// vinte — e essa pessoa não sabe que teve azar.
func main() {
	url, token := os.Args[1], os.Args[2]
	const (
		concorrentes = 10
		porThread    = 30
	)

	var mu sync.Mutex
	tempos := []time.Duration{}
	erros := 0

	var wg sync.WaitGroup
	inicio := time.Now()
	for c := 0; c < concorrentes; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cli := &http.Client{Timeout: 10 * time.Second}
			for i := 0; i < porThread; i++ {
				t0 := time.Now()
				req, _ := http.NewRequest("GET", url, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				res, err := cli.Do(req)
				d := time.Since(t0)
				mu.Lock()
				if err != nil || res.StatusCode != 200 {
					erros++
				} else {
					tempos = append(tempos, d)
				}
				mu.Unlock()
				if res != nil {
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
				}
			}
		}()
	}
	wg.Wait()
	total := time.Since(inicio)

	sort.Slice(tempos, func(i, j int) bool { return tempos[i] < tempos[j] })
	pct := func(p float64) time.Duration {
		if len(tempos) == 0 {
			return 0
		}
		i := int(float64(len(tempos)-1) * p)
		return tempos[i]
	}
	fmt.Printf("  pedidos=%d  erros=%d  em %s (%.0f/s)\n",
		len(tempos), erros, total.Round(time.Millisecond),
		float64(len(tempos))/total.Seconds())
	fmt.Printf("  p50=%s  p95=%s  p99=%s  máx=%s\n",
		pct(0.50).Round(time.Millisecond), pct(0.95).Round(time.Millisecond),
		pct(0.99).Round(time.Millisecond), tempos[len(tempos)-1].Round(time.Millisecond))
	if pct(0.95) > 800*time.Millisecond {
		fmt.Println("  >>> p95 ACIMA de 800 ms")
		os.Exit(1)
	}
	fmt.Println("  p95 dentro dos 800 ms")
}
