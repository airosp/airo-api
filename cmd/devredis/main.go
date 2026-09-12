//go:build devtools

// devredis levanta um Redis em memória para desenvolvimento.
//
// Existe porque as rotas de entrada só se registam com Redis — os limites são
// parte da autenticação, não um extra — e instalar um Redis para experimentar
// a app na própria máquina é atrito sem ganho. É o mesmo `miniredis` que os
// testes usam, o que evita um segundo comportamento a manter.
//
//	go run -tags devtools ./cmd/devredis
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alicebob/miniredis/v2"
)

func main() {
	addr := "127.0.0.1:6399"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	s := miniredis.NewMiniRedis()
	if err := s.StartAddr(addr); err != nil {
		fmt.Fprintln(os.Stderr, "devredis:", err)
		os.Exit(1)
	}
	defer s.Close()
	fmt.Println("redis://" + s.Addr())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
