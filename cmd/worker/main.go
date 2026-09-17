// worker corre o que tem de acontecer sem ninguém pedir.
//
// Faz duas coisas.
//
// **Limpa o que já não serve.** Desafios expirados guardam o hash de um código
// e o número de quem o pediu; tokens revogados guardam a ligação entre um
// aparelho e uma pessoa. Guardá-los depois de deixarem de valer é manter dados
// pessoais sem razão — e a razão é o que a lei pede.
//
// **Vira os ciclos das jornadas.** ⚠️ Um ciclo nascia com a jornada e nunca
// acabava: quem passasse duas semanas sem abrir a app voltava a um plano parado
// no tempo. A jornada tem de avançar porque o tempo passou, e não porque
// alguém tocou no ecrã — que é precisamente o que falha a quem mais precisa
// de voltar.
//
// Separado da API de propósito: um `ticker` dentro do servidor corre em cada
// réplica, e com três réplicas a limpeza corre três vezes. Aqui corre uma.
//
//	AIRO_DATABASE_URL=… go run ./cmd/worker            (uma passagem, e sai)
//	AIRO_DATABASE_URL=… go run ./cmd/worker --repetir  (de hora a hora)
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/airosp/airo-api/internal/platform/config"
	"github.com/airosp/airo-api/internal/platform/logger"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Quanto tempo se guarda cada coisa depois de deixar de valer.
//
// Generoso de propósito: apagar depressa demais tira o rasto de um incidente
// antes de alguém o poder investigar. Uma hora para desafios, trinta dias para
// tokens mortos, noventa para o trilho de auditoria.
const (
	desafiosApos = "1 hour"
	tokensApos   = "30 days"
	eventosApos  = "90 days"
)

func main() {
	repetir := flag.Bool("repetir", false, "correr de hora a hora em vez de uma vez")
	flag.Parse()

	cfg, err := config.Load()
	log := logger.New(os.Getenv("AIRO_ENV"), os.Getenv("AIRO_DEBUG") == "1")
	if err != nil {
		log.Error("configuração", "erro", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("base de dados", "erro", err)
		os.Exit(1)
	}
	defer pool.Close()

	tx := repo.NewTxManager(pool)
	accounts := repo.NewAccountRepo(tx)
	ciclos := repo.NewCiclosRepo(tx)

	passagem := func() {
		// As duas tarefas são independentes: uma falhar não pode impedir a
		// outra de correr. Um worker que desiste à primeira deixa de fazer o
		// trabalho todo por causa de metade.
		if out, err := accounts.Cleanup(ctx, desafiosApos, tokensApos, eventosApos); err != nil {
			log.Error("limpeza falhou", "erro", err)
		} else {
			// Regista sempre, mesmo a zero: um worker silencioso é
			// indistinguível de um worker parado.
			log.Info("limpeza",
				"desafios", out.Challenges, "tokens", out.Tokens, "eventos", out.Events)
		}

		if out, err := ciclos.Rolar(ctx, time.Now().UTC()); err != nil {
			log.Error("virar ciclos falhou", "erro", err)
		} else {
			log.Info("ciclos", "fechados", out.Fechados, "abertos", out.Abertos)
		}
	}

	passagem()
	if !*repetir {
		return
	}

	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("worker a terminar")
			return
		case <-t.C:
			passagem()
		}
	}
}
