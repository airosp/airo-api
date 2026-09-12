// Comando api — o servidor HTTP da Airo.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/airosp/airo-api/internal/platform/config"
	"github.com/airosp/airo-api/internal/platform/logger"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
)

// version é gravada na compilação: -ldflags "-X main.version=$(git rev-parse --short HEAD)"
var version = "dev"

func main() {
	log := logger.New(os.Getenv("AIRO_ENV"), os.Getenv("AIRO_DEBUG") == "1")

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuração inválida", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: airohttp.NewRouter(airohttp.Deps{Log: log, Version: version}),
		// Sem estes prazos, uma ligação lenta segura um descritor para sempre.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		log.Info("a servir", "addr", cfg.HTTPAddr, "env", string(cfg.Env), "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("servidor parou", "error", err)
			os.Exit(1)
		}
	}()

	// Encerramento ordenado: um treino a ser gravado durante um deploy não pode
	// ficar a meio.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Info("a encerrar")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("encerramento forçado", "error", err)
	}
	log.Info("encerrado")
}
