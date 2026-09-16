package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// PantryStore guarda o que é da pessoa à mesa.
type PantryStore interface {
	CustomFoods(ctx context.Context, userID string) ([]repo.CustomFoodRow, error)
	SaveCustomFood(ctx context.Context, userID string, f repo.CustomFoodRow) error
	DeleteCustomFood(ctx context.Context, userID, id string) (bool, error)

	Favourites(ctx context.Context, userID string) ([]repo.FavouriteRow, error)
	SaveFavourite(ctx context.Context, userID string, f repo.FavouriteRow) error
	DeleteFavourite(ctx context.Context, userID, id string) (bool, error)
}

/*
 * Pantry serve o que é da pessoa e não do catálogo.
 *
 * Os alimentos que ela escreveu e as combinações que guardou nasciam no
 * telemóvel e morriam lá. São o tipo de dado que custa a reintroduzir — ninguém
 * se lembra das gramas da receita que escreveu há três meses.
 */
type Pantry struct{ Store PantryStore }

// ── alimentos próprios ──────────────────────────────────────────────────────

func (h Pantry) ListFoods(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	alimentos, err := h.Store.CustomFoods(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os alimentos.")
		return
	}
	out := make([]map[string]any, 0, len(alimentos))
	for _, f := range alimentos {
		out = append(out, map[string]any{
			"id": f.ID, "name": f.Name, "kcal": f.Kcal,
			"macros":  map[string]any{"protein": f.ProteinG, "carbs": f.CarbsG, "fat": f.FatG},
			"serving": f.ServingG,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"foods": out})
}

func (h Pantry) SaveFood(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		apierr.Write(w, apierr.ValidationFailed, "Alimento sem identificador.", "id")
		return
	}

	var req struct {
		Name   string  `json:"name"`
		Kcal   float64 `json:"kcal"`
		Macros struct {
			Protein float64 `json:"protein"`
			Carbs   float64 `json:"carbs"`
			Fat     float64 `json:"fat"`
		} `json:"macros"`
		Serving int `json:"serving"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if req.Name == "" {
		apierr.Write(w, apierr.ValidationFailed, "O alimento precisa de nome.", "name")
		return
	}
	if req.Serving <= 0 {
		apierr.Write(w, apierr.ValidationFailed, "A porção tem de ser positiva.", "serving")
		return
	}
	if req.Kcal < 0 {
		apierr.Write(w, apierr.ValidationFailed, "As calorias não podem ser negativas.", "kcal")
		return
	}

	err := h.Store.SaveCustomFood(r.Context(), userID, repo.CustomFoodRow{
		ID: id, Name: req.Name, Kcal: req.Kcal,
		ProteinG: req.Macros.Protein, CarbsG: req.Macros.Carbs, FatG: req.Macros.Fat,
		ServingG: req.Serving,
	})
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o alimento.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h Pantry) DeleteFood(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	// Apagar o que já não está é o resultado que se queria: 204 nos dois casos.
	if _, err := h.Store.DeleteCustomFood(r.Context(), userID, r.PathValue("id")); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível apagar o alimento.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── favoritas ───────────────────────────────────────────────────────────────

func (h Pantry) ListFavourites(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	favoritas, err := h.Store.Favourites(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as favoritas.")
		return
	}
	out := make([]map[string]any, 0, len(favoritas))
	for _, f := range favoritas {
		linha := map[string]any{
			"id": f.ID, "slot": f.Slot, "title": f.Title, "kcal": f.Kcal,
			"savedAt": f.SavedAt.UTC().Format(time.RFC3339),
		}
		// Os itens vêm como estão: é o cliente que sabe o que são, e reescrevê-los
		// aqui era traduzir duas vezes o mesmo vocabulário.
		linha["items"] = json.RawMessage(f.Items)
		out = append(out, linha)
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"favourites": out})
}

func (h Pantry) SaveFavourite(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		apierr.Write(w, apierr.ValidationFailed, "Favorita sem identificador.", "id")
		return
	}

	var req struct {
		Slot    string          `json:"slot"`
		Title   string          `json:"title"`
		Items   json.RawMessage `json:"items"`
		Kcal    int             `json:"kcal"`
		SavedAt string          `json:"savedAt"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if !slotsValidos[req.Slot] {
		apierr.Write(w, apierr.ValidationFailed, "Refeição desconhecida.", "slot")
		return
	}
	if len(req.Items) == 0 {
		apierr.Write(w, apierr.ValidationFailed, "Uma favorita sem alimentos não é uma favorita.", "items")
		return
	}

	var quando time.Time
	if req.SavedAt != "" {
		t, err := time.Parse(time.RFC3339, req.SavedAt)
		if err != nil {
			apierr.Write(w, apierr.ValidationFailed, "Data inválida.", "savedAt")
			return
		}
		quando = t
	}

	err := h.Store.SaveFavourite(r.Context(), userID, repo.FavouriteRow{
		ID: id, Slot: req.Slot, Title: req.Title, Items: req.Items,
		Kcal: req.Kcal, SavedAt: quando,
	})
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a favorita.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h Pantry) DeleteFavourite(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.quem(w, r)
	if !ok {
		return
	}
	if _, err := h.Store.DeleteFavourite(r.Context(), userID, r.PathValue("id")); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível apagar a favorita.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// quem devolve o utilizador, ou escreve a recusa e devolve falso.
func (h Pantry) quem(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return "", false
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "Indisponível.", "")
		return "", false
	}
	return userID, true
}
