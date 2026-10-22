package intelligence

import (
	"context"
	"sync"
	"time"
)

// Quanto tempo uma execução fica reservada a um processo. Mais do que o prazo
// da execução: um prazo de reserva mais curto deixava outro processo apanhar
// uma execução que ainda está a correr.
const leaseFor = jobTimeout + 30*time.Second

// Quanto tempo se guarda o rasto. Noventa dias de execuções e propostas,
// cento e oitenta de auditoria — o suficiente para investigar uma queixa, e
// não mais do que isso.
const (
	keepJobsFor  = 90 * 24 * time.Hour
	keepAuditFor = 180 * 24 * time.Hour
)

/*
 * Run corre o executor até `ctx` acabar.
 *
 * Dentro da API e não num processo à parte: o serviço que está em produção é
 * um só, e um executor que não corre em lado nenhum deixava todas as
 * fotografias "a analisar" para sempre. Com várias réplicas cada uma corre o
 * seu, e a reserva com `SKIP LOCKED` impede duas de apanharem a mesma
 * execução. A limpeza corre em todas — é idempotente, e correr três vezes um
 * `DELETE` do que já foi apagado não apaga nada.
 */
func (s *Service) Run(ctx context.Context, workers int, poll time.Duration) {
	if !s.cfg.Features.Enabled {
		return
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.loop(ctx, poll)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.retention(ctx)
	}()
	s.log.Info("inteligência: executor a correr", "workers", workers)
	wg.Wait()
	s.log.Info("inteligência: executor parado")
}

func (s *Service) loop(ctx context.Context, poll time.Duration) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		// Esgota a fila antes de esperar: com trabalho acumulado, esperar um
		// tique entre cada execução era atrasar quem está à espera.
		for ctx.Err() == nil && s.RunOnce(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-t.C:
		}
	}
}

// RunOnce reserva e corre uma execução. Devolve se havia alguma.
func (s *Service) RunOnce(ctx context.Context) bool {
	j, err := s.store.ClaimJob(ctx, s.clock.Now(), leaseFor, s.cfg.MaxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("inteligência: não foi possível reservar trabalho", "error", err)
		}
		return false
	}
	if j == nil {
		return false
	}
	s.process(ctx, *j)
	return true
}

func (s *Service) retention(ctx context.Context) {
	run := func() {
		out, err := s.store.ApplyRetention(ctx, s.clock.Now(), keepJobsFor, keepAuditFor)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("inteligência: limpeza falhou", "error", err)
			}
			return
		}
		s.log.Info("inteligência: limpeza",
			"execucoes_expiradas", out.ExpiredJobs, "propostas_expiradas", out.ExpiredProposals,
			"apagadas", out.Deleted, "auditoria_apagada", out.Audit)
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
