package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	"github.com/airosp/airo-api/internal/platform/mux"
)

// ClassVideoStore recebe o que o Mux diz sobre o vídeo de uma aula.
type ClassVideoStore interface {
	MuxPronto(ctx context.Context, aulaID, assetID, playbackID, politica string,
		largura, altura int) (bool, error)
	MuxFalhou(ctx context.Context, aulaID string) (bool, error)
}

/*
 * MuxWebhook recebe as notificações do Mux sobre as aulas.
 *
 * ⚠️ **Carregar um vídeo não o torna reproduzível.** O Mux recebe o ficheiro,
 * transcodifica-o em vários débitos e só então emite o identificador de
 * reprodução. Sem esta rota, saber que uma aula ficou pronta era perguntar em
 * ciclo ou ir à consola do Mux ver à mão — e uma aula publicada antes de o
 * vídeo existir é um leitor a girar para sempre.
 *
 * Quem chama é um servidor do Mux, que não tem sessão na Airo: quem autentica
 * é a assinatura do corpo. Sem segredo configurado a rota não existe — uma
 * rota de webhook aberta deixa qualquer pessoa apontar as nossas aulas aos
 * vídeos dela.
 */
type MuxWebhook struct {
	Store  ClassVideoStore
	Secret string
	Log    *slog.Logger
	Clock  clock.Clock
}

func (h MuxWebhook) Receive(w http.ResponseWriter, r *http.Request) {
	if h.Secret == "" || h.Store == nil {
		http.NotFound(w, r)
		return
	}

	// O corpo em bruto, e limitado. A assinatura é sobre estes bytes: voltar a
	// serializar o JSON muda um espaço e deita-a abaixo.
	corpo, err := io.ReadAll(io.LimitReader(r.Body, maximoBytesDoWebhook))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	agora := time.Now()
	if h.Clock != nil {
		agora = h.Clock.Now()
	}
	if err := mux.VerificarAssinatura(r.Header.Get("Mux-Signature"), corpo, h.Secret, agora); err != nil {
		if h.Log != nil {
			h.Log.Warn("mux: notificação recusada", "error", err)
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	evento, err := mux.LerEvento(corpo)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	/*
	 * Responde-se `200` a tudo o que esteja assinado, mesmo ao que não se
	 * trata.
	 *
	 * O Mux repete o que não recebe `2xx`, com espera crescente, durante um
	 * dia. Um `400` a um evento que simplesmente não nos interessa —
	 * `video.asset.created`, `video.upload.asset_created` — punha-o a repetir
	 * um evento que nunca vai ser tratado.
	 */
	aula := evento.Dados.Passthrough
	switch evento.Tipo {
	case "video.asset.ready":
		if aula == "" {
			h.aviso("mux: recurso pronto sem passthrough", "asset", evento.Dados.ID)
			break
		}
		playback, politica, ok := evento.Playback()
		if !ok {
			h.aviso("mux: recurso pronto sem identificador de reprodução", "aula", aula)
			break
		}
		largura, altura, _ := evento.Dimensoes()
		encontrada, err := h.Store.MuxPronto(r.Context(), aula, evento.Dados.ID, playback, politica,
			largura, altura)
		if err != nil {
			// Um erro nosso **merece** a repetição: devolve-se 500 para o Mux
			// voltar a tentar em vez de dar o evento por entregue.
			h.aviso("mux: gravar recurso pronto", "aula", aula, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !encontrada {
			h.aviso("mux: recurso pronto para uma aula que não existe", "aula", aula)
			break
		}
		// A duração medida fica no registo e não na tabela: é da ficha que
		// ela vem, e ver as duas lado a lado é o que permite descobrir uma
		// aula cuja ficha mente sobre o vídeo.
		h.info("mux: aula pronta", "aula", aula, "playback", playback, "politica", politica,
			"segundos_medidos", int(evento.Dados.Duration))

	case "video.asset.errored":
		if aula == "" {
			break
		}
		if _, err := h.Store.MuxFalhou(r.Context(), aula); err != nil {
			h.aviso("mux: despublicar aula falhada", "aula", aula, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.aviso("mux: o vídeo desta aula não foi processado — aula despublicada", "aula", aula)

	case "video.asset.deleted":
		if aula == "" {
			break
		}
		// Um vídeo apagado no Mux é uma aula sem vídeo. Despublica-se pela
		// mesma razão que a falhada: a ficha fica, o leitor não gira.
		if _, err := h.Store.MuxFalhou(r.Context(), aula); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.info("mux: vídeo apagado — aula despublicada", "aula", aula)
	}

	w.WriteHeader(http.StatusOK)
}

func (h MuxWebhook) aviso(msg string, args ...any) {
	if h.Log != nil {
		h.Log.Warn(msg, args...)
	}
}

func (h MuxWebhook) info(msg string, args ...any) {
	if h.Log != nil {
		h.Log.Info(msg, args...)
	}
}
