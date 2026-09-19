package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// PlaylistStore lê o catálogo de playlists e guarda por onde cada pessoa vai.
type PlaylistStore interface {
	Published(ctx context.Context) ([]repo.PlaylistRow, error)
	Get(ctx context.Context, id string) (repo.PlaylistRow, error)
	Items(ctx context.Context, playlistID string) ([]repo.PlaylistItemRow, error)
	States(ctx context.Context, userID, playlistID string) (map[int]repo.PlaylistStateRow, error)
	SetState(ctx context.Context, userID, playlistID string, position int, status string, watched int) error
	ClearStates(ctx context.Context, userID, playlistID string) error
	ItemClass(ctx context.Context, playlistID string, position int) (repo.ClassRow, error)
}

// PlaylistGoalReader diz qual é o objetivo de quem pergunta, para a lista que
// lhe serve vir à frente.
type PlaylistGoalReader interface {
	GoalOf(ctx context.Context, userID string) (string, error)
}

// Playlists serve sequências de aulas com um objetivo.
//
// ⚠️ **O cliente nunca diz "está feito" a partir de uma conta sua.** Manda os
// segundos que a pessoa viu; quem decide se aquilo conta é este lado, com a
// mesma fracção que decide se um treino contou. Marcar à mão é outra coisa —
// é uma decisão de quem treina, e essa tem rota própria.
type Playlists struct {
	Store PlaylistStore
	Goals PlaylistGoalReader
	// Video monta o endereço de cada aula da lista, a cada pedido.
	Video VideoSource
}

func (h Playlists) quem(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return "", false
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As playlists estão indisponíveis.", "")
		return "", false
	}
	return userID, true
}

// List devolve as playlists publicadas, com a do objetivo da pessoa à frente.
func (h Playlists) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}

	listas, err := h.Store.Published(r.Context())
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler as playlists.", "")
		return
	}

	objetivo := ""
	if h.Goals != nil {
		if g, err := h.Goals.GoalOf(r.Context(), userID); err == nil {
			objetivo = g
		}
	}

	saida := make([]map[string]any, 0, len(listas))
	for _, p := range listas {
		itens, err := h.Store.Items(r.Context(), p.ID)
		if err != nil {
			apierr.Write(w, apierr.Internal, "Não foi possível ler as playlists.", "")
			return
		}
		estados, err := h.Store.States(r.Context(), userID, p.ID)
		if err != nil {
			apierr.Write(w, apierr.Internal, "Não foi possível ler as playlists.", "")
			return
		}
		saida = append(saida, paraPlaylist(p, itens, estados, objetivo, false, h.Video))
	}

	// A que serve o objetivo da pessoa vem primeiro. É ordenação, não filtro:
	// esconder as outras era decidir por ela que não pode mudar de ideias.
	if objetivo != "" {
		ordenadas := make([]map[string]any, 0, len(saida))
		for _, p := range saida {
			if p["forYou"] == true {
				ordenadas = append(ordenadas, p)
			}
		}
		for _, p := range saida {
			if p["forYou"] != true {
				ordenadas = append(ordenadas, p)
			}
		}
		saida = ordenadas
	}

	apierr.WriteJSON(w, http.StatusOK, map[string]any{"playlists": saida, "total": len(saida)})
}

// Get devolve a playlist com as aulas e o ponto em que a pessoa vai.
func (h Playlists) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	p, err := h.Store.Get(r.Context(), id)
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Essa playlist não existe.", "id")
		return
	}
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler a playlist.", "")
		return
	}

	itens, err := h.Store.Items(r.Context(), id)
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler a playlist.", "")
		return
	}
	estados, err := h.Store.States(r.Context(), userID, id)
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler a playlist.", "")
		return
	}

	objetivo := ""
	if h.Goals != nil {
		if g, err := h.Goals.GoalOf(r.Context(), userID); err == nil {
			objetivo = g
		}
	}
	apierr.WriteJSON(w, http.StatusOK, paraPlaylist(p, itens, estados, objetivo, true, h.Video))
}

// posicao lê a posição do caminho, ou responde e devolve falso.
func (h Playlists) posicao(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("position"))
	if err != nil || n < 1 {
		apierr.Write(w, apierr.ValidationFailed, "Posição inválida.", "position")
		return 0, false
	}
	return n, true
}

// Watched recebe **os segundos vistos** e decide se a aula conta.
//
// O cliente não manda veredicto. Isto é o mesmo princípio da gravação de
// sessões — e usa a mesma fracção, `service.CompletionRatio`: duas regras para
// "isto contou" seria a app a dizer uma coisa no histórico e outra na playlist.
//
// Abaixo da fracção não se marca nada: fica por fazer, com os segundos
// guardados. Sair a meio de um vídeo não é saltá-lo, e marcar como saltado quem
// só foi atender o telefone seria decidir por ela.
func (h Playlists) Watched(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	pos, ok := h.posicao(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	var corpo struct {
		Seconds int `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&corpo); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "seconds")
		return
	}
	if corpo.Seconds < 0 {
		apierr.Write(w, apierr.ValidationFailed, "Os segundos vistos não podem ser negativos.", "seconds")
		return
	}

	aula, err := h.Store.ItemClass(r.Context(), id, pos)
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Essa aula não está nesta playlist.", "position")
		return
	}
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível registar.", "")
		return
	}

	// A decisão. Uma linha, como na gravação de sessões.
	contou := aula.DurationSeconds > 0 &&
		float64(corpo.Seconds) >= float64(aula.DurationSeconds)*service.CompletionRatio

	if contou {
		if err := h.Store.SetState(r.Context(), userID, id, pos, "done", corpo.Seconds); err != nil {
			apierr.Write(w, apierr.Internal, "Não foi possível registar.", "")
			return
		}
	}
	h.responderComEstado(w, r, userID, id, contou)
}

// Done marca à mão. É uma decisão de quem treina, não uma conta do cliente —
// e por isso tem rota própria em vez de um campo no corpo de `watched`.
func (h Playlists) Done(w http.ResponseWriter, r *http.Request) {
	h.marcar(w, r, "done")
}

// Skip é "hoje não": fica a marca, a lista pode terminar na mesma, e da
// próxima vez o item volta a aparecer. Quem não quer mesmo voltar a ver um
// exercício exclui-o no plano, que é outro sítio e outra decisão.
func (h Playlists) Skip(w http.ResponseWriter, r *http.Request) {
	h.marcar(w, r, "skipped")
}

func (h Playlists) marcar(w http.ResponseWriter, r *http.Request, estado string) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	pos, ok := h.posicao(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	if _, err := h.Store.ItemClass(r.Context(), id, pos); errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Essa aula não está nesta playlist.", "position")
		return
	} else if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível registar.", "")
		return
	}

	if err := h.Store.SetState(r.Context(), userID, id, pos, estado, 0); err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível registar.", "")
		return
	}
	h.responderComEstado(w, r, userID, id, estado == "done")
}

// Restart devolve a lista ao princípio.
func (h Playlists) Restart(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := h.Store.ClearStates(r.Context(), userID, id); err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível recomeçar.", "")
		return
	}
	h.responderComEstado(w, r, userID, id, false)
}

// responderComEstado devolve sempre a playlist inteira depois de uma escrita.
//
// O ecrã precisa do número novo, de qual é o próximo e de saber se acabou — e
// recalcular isso no cliente era pôr a decisão do lado errado. Uma resposta
// completa poupa o pedido seguinte e garante que os dois lados concordam.
func (h Playlists) responderComEstado(w http.ResponseWriter, r *http.Request, userID, id string, contou bool) {
	p, err := h.Store.Get(r.Context(), id)
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Essa playlist não existe.", "id")
		return
	}
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler a playlist.", "")
		return
	}
	itens, err := h.Store.Items(r.Context(), id)
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler a playlist.", "")
		return
	}
	estados, err := h.Store.States(r.Context(), userID, id)
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível ler a playlist.", "")
		return
	}

	objetivo := ""
	if h.Goals != nil {
		if g, err := h.Goals.GoalOf(r.Context(), userID); err == nil {
			objetivo = g
		}
	}
	saida := paraPlaylist(p, itens, estados, objetivo, true, h.Video)
	saida["counted"] = contou
	apierr.WriteJSON(w, http.StatusOK, saida)
}
