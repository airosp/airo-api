//go:build devtools

// rlclean lista e limpa os contadores de limite de pedidos.
//
// São dados efémeros — uma janela deslizante por eixo — e não têm nada de
// utilizador. Existe porque uma configuração partida gasta o orçamento de quem
// estava a tentar entrar, e essa pessoa fica de fora até a janela passar.
//
//	AIRO_REDIS_URL=… go run -tags devtools ./cmd/rlclean            (lista)
//	AIRO_REDIS_URL=… go run -tags devtools ./cmd/rlclean --apagar   (limpa)
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	url := os.Getenv("AIRO_REDIS_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "falta AIRO_REDIS_URL")
		os.Exit(1)
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	apagar := len(os.Args) > 1 && os.Args[1] == "--apagar"

	var cursor uint64
	total := 0
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "rl:*", 200).Result()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, k := range keys {
			n, _ := rdb.ZCard(ctx, k).Result()
			ttl, _ := rdb.PTTL(ctx, k).Result()
			// A chave traz o número. Mostra-se o eixo e o tamanho, não o resto.
			eixo := strings.TrimPrefix(k, "rl:")
			if i := strings.Index(eixo, ":"); i >= 0 {
				eixo = eixo[:i]
			}
			fmt.Printf("  %-10s usado=%d expira_em=%s\n", eixo, n, ttl.Round(time.Second))
			total++
			if apagar {
				_ = rdb.Del(ctx, k).Err()
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	if apagar {
		fmt.Printf("apagadas %d chaves\n", total)
	} else {
		fmt.Printf("%d chaves (usa --apagar para limpar)\n", total)
	}
}
