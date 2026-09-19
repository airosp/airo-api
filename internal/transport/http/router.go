// Package http monta o encaminhamento.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

type Deps struct {
	Log     *slog.Logger
	Version string
	DB      handlers.Pinger
	Schema  handlers.SchemaChecker
	// Cache é o Redis, para o `/readyz` o poder verificar.
	Cache handlers.Pinger

	// Auth é opcional durante o desenvolvimento: sem ele, as rotas protegidas
	// não são registadas. Nunca ficam abertas — uma rota de escrita sem
	// autenticação é pior do que uma rota que não existe.
	Auth         middleware.TokenVerifier
	AuthAPI      *handlers.Auth
	Goals        *handlers.Goals
	Training     *handlers.Training
	Profile      *handlers.Profile
	Nutrition    *handlers.Nutrition
	Progress     *handlers.Progress
	Account      *handlers.Account
	Catalog      *handlers.Catalog
	Calendar     *handlers.Calendar
	Measurements *handlers.Measurements
	Hydration    *handlers.Hydration
	SessionEdits *handlers.SessionEdits
	Shopping     *handlers.Shopping
	FoodPhotos   *handlers.FoodPhotos
	WhatsApp     *handlers.WhatsAppWebhook
	// Mux recebe as notificações das aulas gravadas. Nil quando não há
	// segredo configurado: a rota não chega a existir.
	Mux         *handlers.MuxWebhook
	Pantry      *handlers.Pantry
	Media       *handlers.Media
	Specialists *handlers.Specialists
	Sessions    *handlers.Sessions
	Classes     *handlers.Classes
	Playlists   *handlers.Playlists
	Idempotency middleware.Store

	// CORSOrigins são as origens do browser autorizadas. Vazio = nenhuma, e a
	// app nativa não precisa de nenhuma.
	CORSOrigins []string
}

// NewRouter devolve o handler raiz com os middlewares já aplicados.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	health := handlers.Health{Version: d.Version, DB: d.DB, Schema: d.Schema, Cache: d.Cache}
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)
	// Os contadores, para o recolector. Números por rota e mais nada — ver o
	// comentário em `middleware/metricas.go`.
	mux.HandleFunc("GET /metrics", middleware.Metrics)

	// As rotas de entrada são públicas por definição: quem ainda não tem sessão
	// não pode provar que a tem. A defesa aqui são os limites, não o token.
	if d.AuthAPI != nil {
		mux.HandleFunc("POST /v1/auth/otp/request", d.AuthAPI.RequestOTP)
		mux.HandleFunc("POST /v1/auth/otp/verify", d.AuthAPI.VerifyOTP)
		mux.HandleFunc("POST /v1/auth/token/refresh", d.AuthAPI.Refresh)
		// Sair é público pela mesma razão que entrar: quem quer sair pode ter o
		// token de acesso já expirado.
		mux.HandleFunc("POST /v1/auth/logout", d.AuthAPI.Logout)
	}

	if d.WhatsApp != nil {
		// Público de propósito: quem o autentica é a assinatura do corpo, não
		// uma sessão. Quem chama é a Meta, que não tem conta nenhuma aqui.
		mux.HandleFunc("GET /v1/webhooks/whatsapp", d.WhatsApp.Verify)
		mux.HandleFunc("POST /v1/webhooks/whatsapp", d.WhatsApp.Receive)
	}
	if d.Mux != nil {
		// Também público, e pela mesma razão: quem chama é um servidor do Mux,
		// e quem o autentica é a assinatura do corpo.
		mux.HandleFunc("POST /v1/webhooks/mux", d.Mux.Receive)
	}

	if d.Auth != nil && (d.Goals != nil || d.Training != nil || d.Profile != nil || d.Nutrition != nil || d.Progress != nil || d.Account != nil || d.Catalog != nil || d.Calendar != nil || d.Measurements != nil || d.Hydration != nil || d.SessionEdits != nil || d.Shopping != nil || d.FoodPhotos != nil || d.Pantry != nil || d.Media != nil || d.Specialists != nil || d.Sessions != nil || d.Classes != nil || d.Playlists != nil) {
		store := d.Idempotency
		if store == nil {
			store = middleware.NewMemoryStore(24 * time.Hour)
		}
		// A cadeia das rotas privadas: autenticar primeiro, e só depois guardar
		// a resposta — a chave de idempotência é por utilizador, e sem saber
		// quem é não há âmbito nenhum.
		private := func(h http.HandlerFunc) http.Handler {
			return middleware.Chain(h,
				middleware.Auth(d.Auth),
				middleware.Idempotency(store, 24*time.Hour),
			)
		}
		if d.Goals != nil {
			mux.Handle("POST /v1/goals", private(d.Goals.Create))
			mux.Handle("POST /v1/goals/assess", private(d.Goals.Assess))
			// Alterar o que está em vigor, sem recomeçar a jornada.
			mux.Handle("PATCH /v1/goals/active",
				middleware.Chain(http.HandlerFunc(d.Goals.Update), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/goals/active",
				middleware.Chain(http.HandlerFunc(d.Goals.Active), middleware.Auth(d.Auth)))
		}
		if d.Training != nil {
			mux.Handle("GET /v1/training/today", private(d.Training.Today))
			mux.Handle("POST /v1/training/sessions", private(d.Training.Record))
			// Abrir a sessão e receber os eventos. O lote é idempotente por
			// construção — os eventos deduplicam-se pelo instante —, por isso
			// fica fora da cadeia de idempotência.
			mux.Handle("POST /v1/training/sessions/open",
				middleware.Chain(http.HandlerFunc(d.Training.Open), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/training/sessions/{id}/events",
				middleware.Chain(http.HandlerFunc(d.Training.Events), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/training/sessions",
				middleware.Chain(http.HandlerFunc(d.Training.History), middleware.Auth(d.Auth)))
			// A carga proposta para hoje, a partir do que ficou registado.
			mux.Handle("GET /v1/training/load-suggestions",
				middleware.Chain(http.HandlerFunc(d.Training.LoadSuggestions), middleware.Auth(d.Auth)))
		}
		if d.Shopping != nil {
			// O que foi riscado e o que foi acrescentado à mão. A lista em si
			// sai do plano — ver o comentário do handler.
			mux.Handle("GET /v1/nutrition/shopping-list/{week}",
				middleware.Chain(http.HandlerFunc(d.Shopping.Get), middleware.Auth(d.Auth)))
			mux.Handle("PUT /v1/nutrition/shopping-list/{week}",
				middleware.Chain(http.HandlerFunc(d.Shopping.Save), middleware.Auth(d.Auth)))
		}
		if d.SessionEdits != nil {
			// O treino do dia como a pessoa o deixou — tirou, trocou, afinou.
			mux.Handle("GET /v1/training/day-edits",
				middleware.Chain(http.HandlerFunc(d.SessionEdits.List), middleware.Auth(d.Auth)))
			mux.Handle("PUT /v1/training/day-edits/{day}",
				middleware.Chain(http.HandlerFunc(d.SessionEdits.Save), middleware.Auth(d.Auth)))
			// O que já deixou de ser uma troca do dia e passou a ser hábito.
			mux.Handle("GET /v1/training/habits",
				middleware.Chain(http.HandlerFunc(d.SessionEdits.Habits), middleware.Auth(d.Auth)))
		}
		if d.Profile != nil {
			mux.Handle("GET /v1/profile", private(d.Profile.Get))
			mux.Handle("PUT /v1/profile", private(d.Profile.Put))
			// A fotografia fica fora da cadeia de idempotência: o corpo é a
			// imagem inteira, e guardar megabytes por chave para responder o
			// mesmo é pagar memória por uma repetição que ninguém faz.
			mux.Handle("POST /v1/profile/photo",
				middleware.Chain(http.HandlerFunc(d.Profile.Photo), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/profile/photo",
				middleware.Chain(http.HandlerFunc(d.Profile.DeletePhoto), middleware.Auth(d.Auth)))
		}
		if d.Classes != nil {
			// Leituras do catálogo de aulas. Fora da idempotência.
			mux.Handle("GET /v1/classes",
				middleware.Chain(http.HandlerFunc(d.Classes.List), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/classes/{id}",
				middleware.Chain(http.HandlerFunc(d.Classes.Get), middleware.Auth(d.Auth)))
		}
		if d.Measurements != nil {
			// A série do corpo. A escrita é idempotente por construção — o
			// mesmo ponto no mesmo dia é o mesmo ponto — e por isso fica fora
			// da cadeia de idempotência, como as das playlists.
			mux.Handle("GET /v1/measurements",
				middleware.Chain(http.HandlerFunc(d.Measurements.List), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/measurements",
				middleware.Chain(http.HandlerFunc(d.Measurements.Add), middleware.Auth(d.Auth)))
		}
		if d.Hydration != nil {
			// O total do dia. A escrita é um `PUT` do total, idempotente por
			// construção, e por isso fora da cadeia de idempotência.
			mux.Handle("GET /v1/hydration",
				middleware.Chain(http.HandlerFunc(d.Hydration.List), middleware.Auth(d.Auth)))
			mux.Handle("PUT /v1/hydration/{day}",
				middleware.Chain(http.HandlerFunc(d.Hydration.Save), middleware.Auth(d.Auth)))
		}
		if d.Pantry != nil {
			// O que é da pessoa à mesa. As escritas são `PUT` com o
			// identificador do telemóvel — idempotentes, e por isso fora da
			// cadeia de idempotência.
			mux.Handle("GET /v1/nutrition/custom-foods",
				middleware.Chain(http.HandlerFunc(d.Pantry.ListFoods), middleware.Auth(d.Auth)))
			mux.Handle("PUT /v1/nutrition/custom-foods/{id}",
				middleware.Chain(http.HandlerFunc(d.Pantry.SaveFood), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/nutrition/custom-foods/{id}",
				middleware.Chain(http.HandlerFunc(d.Pantry.DeleteFood), middleware.Auth(d.Auth)))

			mux.Handle("GET /v1/nutrition/favourites",
				middleware.Chain(http.HandlerFunc(d.Pantry.ListFavourites), middleware.Auth(d.Auth)))
			mux.Handle("PUT /v1/nutrition/favourites/{id}",
				middleware.Chain(http.HandlerFunc(d.Pantry.SaveFavourite), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/nutrition/favourites/{id}",
				middleware.Chain(http.HandlerFunc(d.Pantry.DeleteFavourite), middleware.Auth(d.Auth)))
		}
		if d.Sessions != nil {
			// Onde a conta está aberta, e como fechar. Terminar é idempotente
			// por construção — um aparelho já expulso continua expulso.
			mux.Handle("GET /v1/auth/sessions",
				middleware.Chain(http.HandlerFunc(d.Sessions.List), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/auth/sessions/{id}",
				middleware.Chain(http.HandlerFunc(d.Sessions.Revoke), middleware.Auth(d.Auth)))
		}
		if d.Specialists != nil {
			mux.Handle("GET /v1/specialists",
				middleware.Chain(http.HandlerFunc(d.Specialists.List), middleware.Auth(d.Auth)))
			// A equipa inteira de uma vez: é o estado do ecrã, e mandá-lo torna
			// o reenvio inofensivo. Por isso fora da idempotência.
			mux.Handle("PUT /v1/specialists/team",
				middleware.Chain(http.HandlerFunc(d.Specialists.Save), middleware.Auth(d.Auth)))
		}
		if d.FoodPhotos != nil {
			// As fotografias dos alimentos, resolvidas uma vez para todos.
			// Fora do bloco do acervo de propósito: sem chave configurada isto
			// continua a servir o que já está guardado, que é o caso de quase
			// todos os pedidos.
			mux.Handle("POST /v1/media/food-photos",
				middleware.Chain(http.HandlerFunc(d.FoodPhotos.List), middleware.Auth(d.Auth)))
		}
		if d.Media != nil {
			// O acervo. Leitura pura, e por isso fora da idempotência.
			mux.Handle("GET /v1/media/videos",
				middleware.Chain(http.HandlerFunc(d.Media.Videos), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/media/photos",
				middleware.Chain(http.HandlerFunc(d.Media.Photos), middleware.Auth(d.Auth)))
		}
		if d.Playlists != nil {
			// Leituras do catálogo. Fora da idempotência.
			mux.Handle("GET /v1/playlists",
				middleware.Chain(http.HandlerFunc(d.Playlists.List), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/playlists/{id}",
				middleware.Chain(http.HandlerFunc(d.Playlists.Get), middleware.Auth(d.Auth)))
			/*
			 * As escritas são idempotentes por construção — cada uma põe uma
			 * posição num estado, e repetir põe-na no mesmo. Por isso ficam
			 * fora da cadeia de idempotência: uma chave por toque num botão de
			 * "saltar" seria memória gasta para garantir o que a própria
			 * operação já garante.
			 */
			mux.Handle("POST /v1/playlists/{id}/items/{position}/watched",
				middleware.Chain(http.HandlerFunc(d.Playlists.Watched), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/playlists/{id}/items/{position}/done",
				middleware.Chain(http.HandlerFunc(d.Playlists.Done), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/playlists/{id}/items/{position}/skip",
				middleware.Chain(http.HandlerFunc(d.Playlists.Skip), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/playlists/{id}/progress",
				middleware.Chain(http.HandlerFunc(d.Playlists.Restart), middleware.Auth(d.Auth)))
		}
		if d.Calendar != nil {
			// `PUT` com o identificador do telemóvel: a marca nasce offline e é
			// idempotente por construção, logo fica fora da cadeia.
			mux.Handle("PUT /v1/calendar/marks/{id}",
				middleware.Chain(http.HandlerFunc(d.Calendar.Save), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/calendar/marks",
				middleware.Chain(http.HandlerFunc(d.Calendar.Read), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/calendar/marks/{id}",
				middleware.Chain(http.HandlerFunc(d.Calendar.Delete), middleware.Auth(d.Auth)))
		}
		if d.Catalog != nil {
			// Leituras puras do catálogo: fora da idempotência, e sem tocar em
			// nada de quem pergunta.
			mux.Handle("GET /v1/exercises",
				middleware.Chain(http.HandlerFunc(d.Catalog.Exercises), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/exercises/{id}",
				middleware.Chain(http.HandlerFunc(d.Catalog.Exercise), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/foods",
				middleware.Chain(http.HandlerFunc(d.Catalog.Foods), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/foods/{id}/substitutes",
				middleware.Chain(http.HandlerFunc(d.Catalog.FoodSubstitutes), middleware.Auth(d.Auth)))
		}
		if d.Account != nil {
			// Apagar a conta fica fora da idempotência: apagar duas vezes é
			// apagar uma, e guardar a resposta por chave seria guardar o que
			// já é repetível por natureza.
			mux.Handle("DELETE /v1/account",
				middleware.Chain(http.HandlerFunc(d.Account.Delete), middleware.Auth(d.Auth)))
		}
		if d.Progress != nil {
			// A jornada em curso, como o servidor a guardou desde que o
			// objectivo nasceu. O telemóvel montava a sua.
			mux.Handle("GET /v1/journey",
				middleware.Chain(http.HandlerFunc(d.Progress.Journey), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/progress/snapshot",
				middleware.Chain(http.HandlerFunc(d.Progress.Snapshot), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/plan/week",
				middleware.Chain(http.HandlerFunc(d.Progress.Week), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/progress/adaptations",
				middleware.Chain(http.HandlerFunc(d.Progress.Adaptations), middleware.Auth(d.Auth)))
			// ⚠️ Só a pessoa aplica. A Airo propõe — correr sozinha seria mudar
			// o plano de alguém sem lhe perguntar.
			// Pausar não passa pela idempotência: pausar duas vezes é estar
			// em pausa, e a resposta diz `changed: false`.
			mux.Handle("POST /v1/journeys/{id}/pause",
				middleware.Chain(http.HandlerFunc(d.Progress.Pause), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/journeys/{id}/resume",
				middleware.Chain(http.HandlerFunc(d.Progress.Resume), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/adaptations/{id}/apply",
				middleware.Chain(http.HandlerFunc(d.Progress.Apply), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/adaptations/{id}/dismiss",
				middleware.Chain(http.HandlerFunc(d.Progress.Dismiss), middleware.Auth(d.Auth)))
		}
		if d.Nutrition != nil {
			mux.Handle("POST /v1/nutrition/meal-photo",
				middleware.Chain(http.HandlerFunc(d.Nutrition.MealPhoto), middleware.Auth(d.Auth)))
			// O diário. `PUT` com o identificador no caminho é idempotente por
			// construção, e por isso fica fora da cadeia de idempotência —
			// guardar a resposta por chave seria guardar o que já é repetível.
			mux.Handle("PUT /v1/nutrition/logs/{id}",
				middleware.Chain(http.HandlerFunc(d.Nutrition.SaveLog), middleware.Auth(d.Auth)))
			// O plano do dia não passa pela idempotência: é uma leitura.
			// A avaliação do ciclo: o que os registos dizem sobre o alvo, e a
			// proposta que daí sai — **não aplicada**. Quem aceita é a pessoa.
			mux.Handle("GET /v1/nutrition/assessment",
				middleware.Chain(http.HandlerFunc(d.Nutrition.Assessment), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/nutrition/today",
				middleware.Chain(http.HandlerFunc(d.Nutrition.Today), middleware.Auth(d.Auth)))
			// Trocar uma refeição não passa pela idempotência de propósito:
			// cada toque é um pedido novo — "mostra-me outra" — e repetir tem
			// de dar coisa diferente.
			mux.Handle("POST /v1/nutrition/meals/{slot}/swap",
				middleware.Chain(http.HandlerFunc(d.Nutrition.SwapMeal), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/nutrition/meals/{slot}/swap",
				middleware.Chain(http.HandlerFunc(d.Nutrition.ResetMeal), middleware.Auth(d.Auth)))
			mux.Handle("POST /v1/nutrition/rebalance",
				middleware.Chain(http.HandlerFunc(d.Nutrition.Rebalance), middleware.Auth(d.Auth)))
			mux.Handle("GET /v1/nutrition/logs",
				middleware.Chain(http.HandlerFunc(d.Nutrition.ReadLogs), middleware.Auth(d.Auth)))
			mux.Handle("DELETE /v1/nutrition/logs/{id}",
				middleware.Chain(http.HandlerFunc(d.Nutrition.DeleteLog), middleware.Auth(d.Auth)))
		}
	}

	chain := []func(http.Handler) http.Handler{
		middleware.RequestID,
		middleware.Recover(d.Log),
		middleware.Log(d.Log),
		// Conta tudo, incluindo o que a autenticação recusa: um 401 é tão
		// interessante como um 200 — mais, até, quando são muitos de repente.
		middleware.Metricas(mux),
	}
	if len(d.CORSOrigins) > 0 {
		// Antes do registo: um pedido prévio recusado não é um pedido da
		// aplicação, e enchia o registo com linhas que não dizem nada.
		chain = append([]func(http.Handler) http.Handler{middleware.CORS(d.CORSOrigins)}, chain...)
	}
	return middleware.Chain(mux, chain...)
}
