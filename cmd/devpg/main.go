//go:build devtools

// devpg levanta um Postgres local, para ensaiar o que não se ensaia em
// produção.
//
// É o mesmo `embedded-postgres` dos testes: sem Docker, sem instalação, e sem
// um segundo comportamento a manter.
//
//	go run -tags devtools ./cmd/devpg [porta]
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	embedded "github.com/fergusstrange/embedded-postgres"
)

func main() {
	porta := uint32(54399)
	if len(os.Args) > 1 {
		if v, err := strconv.ParseUint(os.Args[1], 10, 32); err == nil {
			porta = uint32(v)
		}
	}

	pg := embedded.NewDatabase(embedded.DefaultConfig().
		Username("airo").Password("airo").Database("ensaio").Port(porta).
		RuntimePath(fmt.Sprintf("/tmp/pg-ensaio-%d", porta)).
		BinariesPath("/tmp/pg-ensaio-bin"))

	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "devpg:", err)
		os.Exit(1)
	}
	fmt.Printf("postgres://airo:airo@127.0.0.1:%d/ensaio?sslmode=disable\n", porta)

	parar := make(chan os.Signal, 1)
	signal.Notify(parar, os.Interrupt, syscall.SIGTERM)
	<-parar
	_ = pg.Stop()
}
