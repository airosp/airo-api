package handlers

import (
	"context"
	"net/http"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// HydrationStore guarda e lê o total de água por dia.
type HydrationStore interface {
	Set(ctx context.Context, userID string, day time.Time, ml int) error
	Days(ctx context.Context, userID string, from, to time.Time) ([]repo.HydrationDay, error)
}

/*
 * Hydration serve a água bebida.
 *
 * ⚠️ **O alvo não está aqui.** Quem o decide é a nutrição, a partir do peso e
 * do treino do dia, e ele já viaja no `/v1/nutrition/today`. Aqui só mora o que
 * a pessoa bebeu — que era a metade que não saía do telemóvel.
 */
type Hydration struct{ Store HydrationStore }

/** Um limite de sanidade: vinte litros num dia é erro de dedo, não hidratação. */
const maximoMlPorDia = 20000

// List devolve os totais de um intervalo.
func (h Hydration) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "A água está indisponível.", "")
		return
	}

	from, to, err := intervaloDe(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, err.Error(), "from")
		return
	}

	dias, err := h.Store.Days(r.Context(), userID, from, to)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler a água.")
		return
	}

	out := make([]map[string]any, 0, len(dias))
	for _, d := range dias {
		out = append(out, map[string]any{
			"day": d.Day.Format("2006-01-02"), "ml": d.Ml,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"days": out})
}

/*
 * Save grava o total de um dia.
 *
 * `PUT` com o dia no caminho e o **total** no corpo — não um acréscimo. Um
 * "+250" reenviado por uma rede lenta fazia a pessoa beber meio litro sem
 * levantar o copo; um total reenviado é o mesmo total.
 */
func (h Hydration) Save(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "A água está indisponível.", "")
		return
	}

	dia, err := time.Parse("2006-01-02", r.PathValue("day"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "day")
		return
	}

	var req struct {
		Ml int `json:"ml"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if req.Ml < 0 {
		apierr.Write(w, apierr.ValidationFailed, "A água não pode ser negativa.", "ml")
		return
	}
	if req.Ml > maximoMlPorDia {
		apierr.Write(w, apierr.ValidationFailed, "Valor de água acima do possível.", "ml")
		return
	}

	if err := h.Store.Set(r.Context(), userID, dia, req.Ml); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a água.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"day": dia.Format("2006-01-02"), "ml": req.Ml,
	})
}
