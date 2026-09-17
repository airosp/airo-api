package handlers

import (
	"context"
	"net/http"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// SpecialistStore lê o catálogo e guarda a equipa de cada pessoa.
type SpecialistStore interface {
	All(ctx context.Context) ([]repo.SpecialistRow, error)
	Invited(ctx context.Context, userID string) ([]string, error)
	Invite(ctx context.Context, userID string, ids []string) error
}

/*
 * Specialists serve o catálogo e a equipa.
 *
 * ⚠️ O catálogo vivia só no cliente. A pessoa escolhia a Ana no assistente e
 * isso nunca saía do telemóvel: reinstalar apagava a escolha, e o campo
 * `specialist` das aulas era texto que ninguém garantia existir.
 */
type Specialists struct{ Store SpecialistStore }

var papeisDeEspecialista = map[string]bool{
	"trainer": true, "nutritionist": true, "physio": true, "coach": true,
}

var papelPorExtenso = map[string]string{
	"trainer": "Personal trainer", "nutritionist": "Nutricionista",
	"physio": "Fisioterapeuta", "coach": "Coach de hábitos",
}

// List devolve o catálogo, com a equipa de quem pergunta marcada.
func (h Specialists) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "Os especialistas estão indisponíveis.", "")
		return
	}

	lista, err := h.Store.All(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os especialistas.")
		return
	}

	// A equipa entra no mesmo pedido: o ecrã mostra as duas coisas ao mesmo
	// tempo e dois pedidos dariam um instante com o catálogo já desenhado e
	// ninguém escolhido.
	equipa, err := h.Store.Invited(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler a tua equipa.")
		return
	}
	convidado := map[string]bool{}
	for _, id := range equipa {
		convidado[id] = true
	}

	out := make([]map[string]any, 0, len(lista))
	for _, s := range lista {
		out = append(out, map[string]any{
			"id": s.ID, "name": s.Name, "role": s.Role,
			"roleLabel": papelDeEspecialistaPorExtenso(s.Role),
			"headline":  s.Headline, "bio": s.Bio,
			"tags": nonNilStrings(s.Tags), "rating": s.Rating, "clients": s.Clients,
			"responseTime": s.ResponseTime, "initials": s.Initials,
			"gradient":       nonNilStrings(s.Gradient),
			"recommendedFor": nonNilStrings(s.RecommendedFor),
			"invited":        convidado[s.ID],
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"specialists": out, "team": nonNilStrings(equipa),
	})
}

/*
 * Save grava a equipa.
 *
 * `PUT` com a equipa inteira, e não um convite de cada vez: é o estado que o
 * ecrã tem, e mandar o estado faz de reenviar uma operação inofensiva.
 */
func (h Specialists) Save(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "Os especialistas estão indisponíveis.", "")
		return
	}

	var req struct {
		Team []string `json:"team"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	// Uma equipa vazia é um estado válido: o plano funciona sem ninguém.
	if len(req.Team) > 4 {
		apierr.Write(w, apierr.ValidationFailed,
			"Só há quatro papéis: treino, nutrição, recuperação e hábitos.", "team")
		return
	}

	if err := h.Store.Invite(r.Context(), userID, req.Team); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a tua equipa.")
		return
	}

	// Devolve-se o que ficou, não o que foi pedido: um identificador que não
	// existe é ignorado em silêncio pela gravação, e o ecrã tem de o saber.
	equipa, err := h.Store.Invited(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler a tua equipa.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"team": nonNilStrings(equipa)})
}

func papelDeEspecialistaPorExtenso(p string) string {
	if v, ok := papelPorExtenso[p]; ok {
		return v
	}
	return p
}
