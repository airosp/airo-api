// Comando api — o servidor HTTP da Airo.
//
// Aceita um subcomando de migração para os casos em que se quer aplicar ou
// desfazer o esquema sem levantar o servidor:
//
//	airo-api migrate up
//	airo-api migrate down
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
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/migrations"
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

	ctx := context.Background()
	pool, err := airopg.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("base de dados", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		log.Error("migrações ilegíveis", "error", err)
		os.Exit(1)
	}

	if len(os.Args) > 2 && os.Args[1] == "migrate" {
		switch os.Args[2] {
		case "up":
			if err := airopg.Up(ctx, pool, migs, log); err != nil {
				log.Error("migrar", "error", err)
				os.Exit(1)
			}
		case "down":
			if err := airopg.Down(ctx, pool, migs, log); err != nil {
				log.Error("desfazer", "error", err)
				os.Exit(1)
			}
		default:
			log.Error("subcomando desconhecido", "arg", os.Args[2])
			os.Exit(2)
		}
		log.Info("migrações concluídas")
		return
	}

	// No arranque normal as migrações correm na mesma, com cadeado: num deploy
	// com duas instâncias, a segunda espera em vez de aplicar o mesmo DDL.
	if err := airopg.Up(ctx, pool, migs, log); err != nil {
		log.Error("migrar no arranque", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: airohttp.NewRouter(airohttp.Deps{Log: log, Version: version, DB: pool}),
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
	shutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("encerramento forçado", "error", err)
	}
	log.Info("encerrado")
}
