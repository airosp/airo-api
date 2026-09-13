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
	"github.com/airosp/airo-api/internal/platform/cloudinary"
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

	// Images guarda as fotografias de perfil. Nil desliga a funcionalidade.
	Images *cloudinary.Client
	// MealImages guarda as fotografias de refeições, noutra pasta.
	MealImages *cloudinary.Client
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

	goals := repo.NewGoalRepo(tx)
	goalSvc := service.NewGoalService(tx, goals, configs, p.Clock)
	sessions := repo.NewSessionRepo(tx, catalog)
	trainingSvc := service.NewTrainingService(sessions, trainingCfg, p.Clock)

	// Sem Cloudinary configurada, o perfil funciona e a fotografia não: é
	// melhor do que a API não arrancar por causa de um avatar.
	var uploader service.Uploader
	if p.Images != nil {
		uploader = avatarUploader{c: p.Images}
	}
	prefs := repo.NewPreferenceRepo(tx)
	profiles := service.NewProfiles(repo.NewProfileRepo(tx), uploader, prefs)
	deps.Goals = &handlers.Goals{Service: goalSvc, Profiles: profiles, Reader: goals}
	deps.Training = &handlers.Training{Service: trainingSvc, Profiles: profiles, Sessions: sessions}
	deps.Profile = &handlers.Profile{Profiles: profiles, Clock: p.Clock}
	// A rota existe sempre; o que muda é a resposta. Sem Cloudinary, diz que
	// as fotografias estão indisponíveis — que é informação. Não a registar
	// dava 404, e um 404 numa rota que existe no contrato manda quem a chama
	// procurar do lado errado.
	var refeicoes handlers.MealUploader
	if p.MealImages != nil {
		refeicoes = mealUploader{c: p.MealImages}
	}
	nutritionSvc := service.NewNutritionService(goals, repo.NewMealPrefRepo(tx), configs.Nutrition, configs.Goal)
	deps.Nutrition = &handlers.Nutrition{
		Photos: refeicoes, Logs: repo.NewNutritionRepo(tx),
		Plans: nutritionSvc, Profiles: profiles,
	}

	deps.Progress = &handlers.Progress{
		Service: service.NewProgressService(repo.NewProgressRepo(tx), goals, configs.Journey).
			WithAdaptations(repo.NewAdaptationRepo(tx), goals, repo.NewProfileRepo(tx)),
		Profiles: profiles,
		Clock:    p.Clock,
	}

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
// ⚠️ **Só em desenvolvimento.** O `main` recusa arrancar em produção sem canal
// a sério: um código de autenticação em registos é o mesmo que não ter código.
// Ver a lista de verificação em docs/backend/08-autenticacao.md §14.
//
// O código aparece por inteiro, e é deliberado: sem ele não há forma de
// percorrer a entrada de ponta a ponta numa máquina de trabalho, e uma entrada
// que nunca se percorreu inteira é uma entrada por testar.
type logSender struct{ log *slog.Logger }

func NewLogSender(log *slog.Logger) service.Sender { return logSender{log: log} }

func (s logSender) Send(_ context.Context, phone, code, channel string) (string, error) {
	// A chave não é `code`: o `scrub` do registo oculta essa, e bem — é o que
	// impede um código de escorregar para os registos de produção por
	// distracção. Aqui o nome diz ao que vem, e este remetente não existe fora
	// de desenvolvimento.
	s.log.Warn("SEM CANAL DE ENVIO — código no registo, só em desenvolvimento",
		"phone", auth.MaskPhone(phone), "channel", channel, "codigo_desenvolvimento", code)
	return "dev-" + time.Now().Format("150405"), nil
}
