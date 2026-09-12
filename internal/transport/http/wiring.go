package http

import (
	"context"
	"log/slog"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Wire monta a API inteira a partir do que a plataforma dá.
//
// Existe porque o `main` estava a construir o router **sem handler nenhum**: as
// rotas só se registam quando as dependências chegam, e ninguém as passava. A
// API subia, respondia `/healthz`, e devolvia 404 a tudo o resto — sem nada a
// dizer que faltava alguma coisa.
//
// Juntar o wiring num sítio só torna esse esquecimento impossível de repetir:
// falta uma dependência, não compila.
type Platform struct {
	Log     *slog.Logger
	Version string
	Pool    *pgxpool.Pool
	Redis   redis.UniversalClient
	Clock   clock.Clock

	JWTSecret []byte
	OTPPepper []byte

	// Sender entrega o código. Sem ele, a API sobe sem autenticação — e é dito
	// em voz alta, em vez de as rotas desaparecerem em silêncio.
	Sender service.Sender
}

func Wire(p Platform) Deps {
	tx := repo.NewTxManager(p.Pool)
	catalog := repo.NewCatalogRepo(tx)

	configs := service.Configs{
		Goal:      goal.DefaultConfig(),
		Journey:   journey.DefaultConfig(),
		Nutrition: nutrition.DefaultConfig(),
	}
	trainingCfg := training.DefaultConfig()

	deps := Deps{
		Log:     p.Log,
		Version: p.Version,
		DB:      p.Pool,
	}

	goalSvc := service.NewGoalService(tx, repo.NewGoalRepo(tx), configs, p.Clock)
	trainingSvc := service.NewTrainingService(repo.NewSessionRepo(tx, catalog), trainingCfg, p.Clock)

	profiles := service.NewProfiles(repo.NewProfileRepo(tx))
	deps.Goals = &handlers.Goals{Service: goalSvc, Profiles: profiles}
	deps.Training = &handlers.Training{Service: trainingSvc, Profiles: profiles}

	if len(p.JWTSecret) >= 32 {
		tokens := auth.NewTokenIssuer(p.JWTSecret, p.Clock.Now)
		deps.Auth = tokens

		if p.Redis != nil && len(p.OTPPepper) >= 32 && p.Sender != nil {
			deps.AuthAPI = &handlers.Auth{
				Service: service.NewAuthService(
					repo.NewAuthRepo(tx), auth.NewRedisLimiter(p.Redis), p.Sender,
					service.DefaultAuthConfig(p.OTPPepper), p.Clock),
				Tokens: tokens,
			}
		} else {
			p.Log.Warn("autenticação por telefone desligada: falta Redis, pepper ou canal de envio")
		}
	} else {
		p.Log.Warn("sem AIRO_JWT_SECRET: as rotas privadas não são registadas")
	}

	deps.Idempotency = middleware.NewMemoryStore(24 * time.Hour)
	return deps
}

// logSender escreve o código no registo em vez de o enviar.
//
// ⚠️ **Só em desenvolvimento.** Em produção é recusado no arranque: um código
// de autenticação em registos é o mesmo que não ter código. Ver a lista de
// verificação em docs/backend/08-autenticacao.md §14.
type logSender struct{ log *slog.Logger }

func NewLogSender(log *slog.Logger) service.Sender { return logSender{log: log} }

func (s logSender) Send(_ context.Context, phone, code, channel string) (string, error) {
	s.log.Warn("canal de envio por escrever — código não enviado",
		"phone", auth.MaskPhone(phone), "channel", channel, "code_length", len(code))
	return "dev-" + time.Now().Format("150405"), nil
}
