package handlers

import (
	"context"
	"net/http"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// CalendarStore guarda as marcas do calendário.
type CalendarStore interface {
	Save(ctx context.Context, userID string, m repo.MarkRow) error
	Marks(ctx context.Context, userID string, from, to time.Time) ([]repo.MarkRow, error)
	Delete(ctx context.Context, userID, id string) (bool, error)
}

type Calendar struct{ Marks CalendarStore }

var tiposDeMarca = map[string]bool{
	"workout": true, "rest": true, "absence": true, "note": true,
}

// Save grava uma marca.
//
// `PUT` com o identificador no caminho: a marca nasce no telemóvel, muitas
// vezes sem rede, e é ele que lhe dá o nome. Mandar a mesma duas vezes é mandar
// a mesma marca.
func (h Calendar) Save(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Marks == nil {
		apierr.Write(w, apierr.Internal, "O calendário está indisponível.", "")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		apierr.Write(w, apierr.ValidationFailed, "Marca sem identificador.", "id")
		return
	}

	var req struct {
		Kind  string `json:"kind"`
		Day   string `json:"day"`
		Until string `json:"until,omitempty"`
		Text  string `json:"text,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if !tiposDeMarca[req.Kind] {
		apierr.Write(w, apierr.ValidationFailed, "Tipo de marca desconhecido.", "kind")
		return
	}

	day, err := time.Parse("2006-01-02", req.Day)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "day")
		return
	}
	until := day
	if req.Until != "" {
		until, err = time.Parse("2006-01-02", req.Until)
		if err != nil {
			apierr.Write(w, apierr.ValidationFailed, "Dia final inválido.", "until")
			return
		}
	}
	if until.Before(day) {
		apierr.Write(w, apierr.ValidationFailed, "O fim é antes do início.", "until")
		return
	}
	// Só as ausências se estendem: uma nota de três dias seriam três notas, e
	// um "descanso" de uma semana é o plano a mudar, não uma marca.
	if req.Kind != "absence" && !until.Equal(day) {
		apierr.Write(w, apierr.ValidationFailed, "Só as ausências ocupam um intervalo.", "until")
		return
	}

	if err := h.Marks.Save(r.Context(), userID, repo.MarkRow{
		ID: id, Kind: req.Kind, Day: day, Until: until, Text: req.Text,
	}); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a marca.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Read devolve as marcas de um intervalo.
func (h Calendar) Read(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Marks == nil {
		apierr.Write(w, apierr.Internal, "O calendário está indisponível.", "")
		return
	}

	from, to, err := intervaloDe(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, err.Error(), "from")
		return
	}

	marcas, err := h.Marks.Marks(r.Context(), userID, from, to)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o calendário.")
		return
	}

	out := make([]map[string]any, 0, len(marcas))
	for _, m := range marcas {
		linha := map[string]any{
			"id": m.ID, "kind": m.Kind, "day": m.Day.Format("2006-01-02"),
		}
		// `until` só quando difere: mandá-lo sempre fazia uma nota parecer um
		// intervalo de um dia, que é uma coisa que não existe.
		if !m.Until.Equal(m.Day) {
			linha["until"] = m.Until.Format("2006-01-02")
		}
		if m.Text != "" {
			linha["text"] = m.Text
		}
		out = append(out, linha)
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"marks": out})
}

// Delete apaga uma marca.
func (h Calendar) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Marks == nil {
		apierr.Write(w, apierr.Internal, "O calendário está indisponível.", "")
		return
	}
	// Apagar o que já não existe não é erro: quem pediu queria que deixasse de
	// estar lá, e não está.
	if _, err := h.Marks.Delete(r.Context(), userID, r.PathValue("id")); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível apagar a marca.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// intervaloDe lê `from` e `to` da consulta, inclusive nas duas pontas.
//
// Com um tecto: um intervalo sem limite é um pedido que cresce com a conta, e
// a primeira pessoa com dois anos de histórico descobre-o pelo tempo de espera.
func intervaloDe(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	from, err := time.Parse("2006-01-02", q.Get("from"))
	if err != nil {
		return time.Time{}, time.Time{}, errIntervalo
	}
	to, err := time.Parse("2006-01-02", q.Get("to"))
	if err != nil {
		return time.Time{}, time.Time{}, errIntervalo
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, errIntervalo
	}
	if to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, errLongoDemais
	}
	return from, to, nil
}

var (
	errIntervalo   = erroSimples("Intervalo inválido.")
	errLongoDemais = erroSimples("Intervalo longo demais — no máximo um ano.")
)

type erroSimples string

func (e erroSimples) Error() string { return string(e) }
