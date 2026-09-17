package handlers

import (
	"context"
	"net/http"

	"github.com/airosp/airo-api/internal/platform/pexels"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// MediaSource procura vídeos e fotografias no acervo.
type MediaSource interface {
	SearchVideos(ctx context.Context, query string, perPage int) ([]pexels.Video, error)
	SearchPhotos(ctx context.Context, query string, perPage int) ([]pexels.Photo, error)
	Configured() bool
}

/*
 * Media serve as imagens do acervo, com a chave deste lado.
 *
 * ⚠️ A chave estava na app, com prefixo `EXPO_PUBLIC_` — embutida em cada build
 * e extraível de um `.apk`. Quem a extraísse gastava a quota alheia e assinava
 * os pedidos como se fosse a Airo. Aqui fica no servidor, e o telemóvel pede
 * ao servidor com a sessão dele.
 */
type Media struct{ Source MediaSource }

func (h Media) Videos(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Source == nil || !h.Source.Configured() {
		// Sem acervo configurado a app desenha o gradiente por categoria. Não é
		// erro do servidor; é uma imagem que não há.
		apierr.WriteJSON(w, http.StatusOK, map[string]any{"videos": []any{}})
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		apierr.Write(w, apierr.ValidationFailed, "Falta o que procurar.", "query")
		return
	}

	videos, err := h.Source.SearchVideos(r.Context(), query, inteiroOuZero(r.URL.Query().Get("perPage")))
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível procurar vídeos.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"videos": videos})
}

func (h Media) Photos(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Source == nil || !h.Source.Configured() {
		apierr.WriteJSON(w, http.StatusOK, map[string]any{"photos": []any{}})
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		apierr.Write(w, apierr.ValidationFailed, "Falta o que procurar.", "query")
		return
	}

	fotos, err := h.Source.SearchPhotos(r.Context(), query, inteiroOuZero(r.URL.Query().Get("perPage")))
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível procurar fotografias.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"photos": fotos})
}

// inteiroOuZero devolve zero para o que não for número — e zero quer dizer
// "decide tu", que é o que o acervo faz com uma página por omissão.
func inteiroOuZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 100 {
			return 0
		}
	}
	return n
}
