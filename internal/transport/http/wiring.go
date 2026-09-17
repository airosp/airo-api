package http

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	"github.com/airosp/airo-api/internal/platform/cloudinary"
	"github.com/airosp/airo-api/internal/platform/pexels"
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

	/*
	 * WhatsAppWebhookSecret é o app secret com que a Meta assina os eventos de
	 * entrega. Vazio não regista a rota: uma rota de webhook que aceite corpos
	 * não assinados é pior do que rota nenhuma.
	 */
	WhatsAppWebhookSecret string

	// Images guarda as fotografias de perfil. Nil desliga a funcionalidade.
	Images *cloudinary.Client
	// MealImages guarda as fotografias de refeições, noutra pasta.
	MealImages *cloudinary.Client

	// Stock é o acervo de vídeos e fotografias. Nil, ou sem chave, deixa a app
	// com os gradientes por categoria — que é o que ela já faz sem rede.
	Stock *pexels.Client
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
	if p.Redis != nil {
		deps.Cache = redisPinger{c: p.Redis}
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
	// A equipa entra aqui: o treinador escolhido muda o treino que o motor
	// monta — ver `training/metodo.go`.
	profiles := service.NewProfiles(repo.NewProfileRepo(tx), uploader, prefs).
		ComEquipa(repo.NewSpecialistRepo(tx))
	deps.Goals = &handlers.Goals{Service: goalSvc, Profiles: profiles, Reader: goals, Editor: goals}
	deps.Training = &handlers.Training{
		Service: trainingSvc, Profiles: profiles, Sessions: sessions,
		Classes:  repo.NewClassRepo(tx),
		Training: trainingCfg,
	}
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

	// Sem Cloudinary a conta apaga-se na mesma: uma imagem órfã limpa-se
	// depois, uma conta que não se consegue apagar não.
	var apagaImagens service.ImageDeleter
	if p.Images != nil {
		apagaImagens = avatarUploader{c: p.Images}
	}
	deps.Classes = &handlers.Classes{Store: repo.NewClassRepo(tx), Profiles: profiles}
	// As playlists lêem o objetivo activo para pôr à frente a que serve quem
	// pergunta — por isso partilham o repositório dos objetivos.
	deps.Playlists = &handlers.Playlists{Store: repo.NewPlaylistRepo(tx), Goals: goals}
	deps.Calendar = &handlers.Calendar{Marks: repo.NewCalendarRepo(tx)}
	// A série do corpo sai do mesmo repositório que o progresso: é a mesma
	// tabela, lida de duas maneiras — aqui ponto a ponto, lá como tendência.
	deps.Measurements = &handlers.Measurements{Store: repo.NewProgressRepo(tx)}
	deps.Hydration = &handlers.Hydration{Store: repo.NewHydrationRepo(tx)}
	deps.SessionEdits = &handlers.SessionEdits{Store: repo.NewSessionEditRepo(tx)}
	deps.Shopping = &handlers.Shopping{Store: repo.NewShoppingRepo(tx)}
	// O acervo entra quando há chave; sem ela, isto serve só o que já está
	// guardado — que continua a ser a resposta certa para quase todos os
	// pedidos, porque a imagem já foi encontrada por outra pessoa.
	fotos := &handlers.FoodPhotos{Store: repo.NewMediaRepo(tx)}
	if p.Stock != nil {
		fotos.Source = p.Stock
	}
	deps.FoodPhotos = fotos
	deps.Pantry = &handlers.Pantry{Store: repo.NewPantryRepo(tx)}
	deps.Specialists = &handlers.Specialists{Store: repo.NewSpecialistRepo(tx)}
	deps.Sessions = &handlers.Sessions{Devices: repo.NewAuthRepo(tx), Clock: p.Clock}
	/*
	 * O acervo regista-se **sempre**, com ou sem chave.
	 *
	 * ⚠️ Antes só existia quando havia chave configurada, e isso quer dizer que
	 * a forma da API mudava com a configuração: a mesma app recebia 404 numa
	 * instalação e 200 noutra, e o cliente não tem como saber qual é qual.
	 * Sem chave o handler já devolve uma lista vazia — que é uma resposta, e é
	 * a que a app sabe desenhar.
	 */
	media := &handlers.Media{}
	if p.Stock != nil {
		media.Source = p.Stock
	}
	deps.Media = media
	deps.Catalog = &handlers.Catalog{Training: trainingCfg, Nutrition: configs.Nutrition}
	deps.Account = &handlers.Account{
		Service: service.NewAccountService(repo.NewAccountRepo(tx), apagaImagens),
	}

	deps.Progress = &handlers.Progress{
		Service: service.NewProgressService(repo.NewProgressRepo(tx), goals, configs.Journey).
			WithAdaptations(repo.NewAdaptationRepo(tx), goals, repo.NewProfileRepo(tx)).
			WithAbsences(repo.NewCalendarRepo(tx)),
		Profiles: profiles,
		Clock:    p.Clock,
		Marks:    repo.NewCalendarRepo(tx),
		Sessions: sessions,
		Training: trainingCfg,
	}

	if len(p.JWTSecret) >= 32 {
		tokens := auth.NewTokenIssuer(p.JWTSecret, p.Clock.Now)
		deps.Auth = tokens

		if p.Redis != nil && len(p.OTPPepper) >= 32 && p.Sender != nil {
			deps.AuthAPI = &handlers.Auth{
				Service: service.NewAuthService(
					repo.NewAuthRepo(tx), auth.NewRedisLimiter(p.Redis), p.Sender,
					configuracaoDeAutenticacao(p.OTPPepper), p.Clock),
				Tokens: tokens,
			}
		} else {
			p.Log.Warn("autenticação por telefone desligada: falta Redis, pepper ou canal de envio")
		}

		// O webhook de entrega. Sem segredo não se regista rota nenhuma: uma
		// rota de webhook aberta é pior do que rota nenhuma.
		if p.WhatsAppWebhookSecret != "" {
			deps.WhatsApp = &handlers.WhatsAppWebhook{
				Store:       repo.NewAuthRepo(tx),
				Secret:      p.WhatsAppWebhookSecret,
				VerifyToken: os.Getenv("AIRO_WHATSAPP_VERIFY_TOKEN"),
			}
		} else {
			p.Log.Warn("sem AIRO_WHATSAPP_WEBHOOK_SECRET: as confirmações de entrega não são recebidas")
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

// redisPinger deixa o `/readyz` verificar o Redis sem o handler saber que
// existe um cliente de Redis.
type redisPinger struct{ c redis.UniversalClient }

func (r redisPinger) Ping(ctx context.Context) error { return r.c.Ping(ctx).Err() }

/*
 * configuracaoDeAutenticacao é a configuração por omissão, com o prazo da
 * sessão ajustável.
 *
 * `AIRO_SESSION_MAX_AGE` existe para se poder encurtar sem recompilar — em
 * ensaio, para ver o ecrã que aparece a quem tem de confirmar o número outra
 * vez; e em produção, se um dia sessenta dias se revelarem de mais. Ausente ou
 * ilegível fica o que estava: um prazo mal escrito não pode ser um prazo
 * infinito.
 */
func configuracaoDeAutenticacao(pepper []byte) service.AuthConfig {
	cfg := service.DefaultAuthConfig(pepper)
	if v := os.Getenv("AIRO_SESSION_MAX_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.SessionMaxAge = d
		}
	}
	return cfg
}
