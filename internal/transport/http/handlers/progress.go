package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/internal/transport/http/view"
)

// ProgressProfileReader dá o plano contra o qual a adesão se mede.
type ProgressProfileReader interface {
	Profile(ctx contextLike, userID string) (service.CreateGoalInput, error)
	TrainingDaysOf(ctx contextLike, userID string) ([]int, error)
}

type Progress struct {
	Service  *service.ProgressService
	Profiles ProgressProfileReader
	Clock    clock.Clock
}

// Snapshot devolve o retrato do progresso.
//
// ⚠️ **A forma muda com o horizonte.** Num horizonte fechado há tendência,
// previsão e a distância ao alvo; num aberto há consistência, ciclo e
// progressão. Não é a mesma resposta com campos vazios — é outra pergunta, e
// misturá-las dava um ecrã com metade dos espaços em branco.
func (h Progress) Snapshot(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Service == nil || h.Profiles == nil {
		apierr.Write(w, apierr.Internal, "O progresso está indisponível.", "")
		return
	}

	perfil, err := h.Profiles.Profile(r.Context(), userID)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	case err != nil && !errors.Is(err, service.ErrWeightMissing):
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return
	}

	dias, err := h.Profiles.TrainingDaysOf(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu plano.")
		return
	}

	now := time.Now().UTC()
	if h.Clock != nil {
		now = h.Clock.Now().UTC()
	}

	snap, err := h.Service.Snapshot(r.Context(), service.SnapshotInput{
		UserID:         userID,
		DaysPerWeek:    perfil.DaysPerWeek,
		SessionMinutes: perfil.SessionMinutes,
		TrainingDays:   dias,
		WeightKg:       perfil.CurrentWeightKg,
		Now:            now,
	})
	switch {
	case errors.Is(err, service.ErrSemJornada):
		// 404 e não erro: quem ainda não escolheu objectivo não tem progresso a
		// medir, e isso é uma resposta — não uma falha.
		apierr.Write(w, apierr.NotFound, "Ainda não tens um objectivo em curso.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível montar o teu progresso.")
		return
	}

	apierr.WriteJSON(w, http.StatusOK, view.BuildProgressSnapshot(paraVista(snap)))
}

// paraVista traduz o resultado do serviço no que a vista desenha.
//
// A tradução vive aqui e não na vista porque o `view` é importado pelo serviço:
// depender de volta fecharia um ciclo que o compilador recusa — e a fronteira
// que isso protege é a que mantém a apresentação substituível.
func paraVista(s service.Snapshot) view.SnapshotData {
	out := view.SnapshotData{
		JourneyID:   s.JourneyID,
		Paused:      s.Paused,
		PausedSince: s.PausedSince,
		PausedDays:  s.PausedDays,
		Horizon:     s.Horizon,
		MetricKey:   s.MetricKey,
		Trend:       s.Trend,
		Adherence:   s.Adherence,
		Risks:       s.Risks,
	}
	if s.Forecast != nil {
		out.TemPrevisao = true
		out.ForecastOn = s.Forecast.ExpectedOn
		out.ForecastConfidence = s.Forecast.Confidence
	}
	if s.Cycle != nil {
		out.CycleIndex = s.Cycle.Index
		d := s.Cycle.ReviewDate
		out.CycleReview = &d
	}
	if s.Consistency != nil {
		out.TemConsistencia = true
		out.SessionsPlanned = s.Consistency.SessionsPlanned
		out.SessionsDone = s.Consistency.SessionsDone
		out.ConsistencyRate = s.Consistency.Rate
	}
	if s.Progression != nil {
		out.VolumeTrend = s.Progression.VolumeTrend
	}
	return out
}

// Adaptations devolve as propostas pendentes.
func (h Progress) Adaptations(w http.ResponseWriter, r *http.Request) {
	userID, in, ok := h.contexto(w, r)
	if !ok {
		return
	}
	linhas, err := h.Service.Adaptations(r.Context(), in)
	switch {
	case errors.Is(err, service.ErrSemJornada):
		apierr.Write(w, apierr.NotFound, "Ainda não tens um objectivo em curso.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler as propostas.")
		return
	}
	_ = userID
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"adaptations": view.BuildAdaptations(paraVistaAdaptacoes(linhas)),
	})
}

// Apply aceita a proposta.
//
// ⚠️ **Só a pessoa aplica.** A Airo propõe; correr sozinha no fim de um ciclo
// seria mudar o plano de alguém sem lhe perguntar.
func (h Progress) Apply(w http.ResponseWriter, r *http.Request) { h.decidir(w, r, true) }

// Dismiss recusa a proposta. Recusada não volta a aparecer — uma sugestão que
// reaparece depois de dispensada deixa de ser sugestão.
func (h Progress) Dismiss(w http.ResponseWriter, r *http.Request) { h.decidir(w, r, false) }

func (h Progress) decidir(w http.ResponseWriter, r *http.Request, aplicar bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Service == nil {
		apierr.Write(w, apierr.Internal, "As propostas estão indisponíveis.", "")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		apierr.Write(w, apierr.ValidationFailed, "Proposta desconhecida.", "id")
		return
	}

	row, err := h.Service.DecideAdaptation(r.Context(), userID, id, aplicar)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		// Também é o que responde a quem tenta decidir a proposta de outra
		// pessoa: dizer "não tens acesso" confirmaria que ela existe.
		apierr.Write(w, apierr.NotFound, "Essa proposta já não está pendente.", "id")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível registar a tua decisão.")
		return
	}

	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"id": row.ID, "kind": row.Kind, "applied": aplicar,
	})
}

// contexto reúne o que os dois endpoints de leitura precisam.
func (h Progress) contexto(w http.ResponseWriter, r *http.Request) (string, service.SnapshotInput, bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return "", service.SnapshotInput{}, false
	}
	if h.Service == nil || h.Profiles == nil {
		apierr.Write(w, apierr.Internal, "O progresso está indisponível.", "")
		return "", service.SnapshotInput{}, false
	}

	perfil, err := h.Profiles.Profile(r.Context(), userID)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return "", service.SnapshotInput{}, false
	case err != nil && !errors.Is(err, service.ErrWeightMissing):
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return "", service.SnapshotInput{}, false
	}

	dias, err := h.Profiles.TrainingDaysOf(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu plano.")
		return "", service.SnapshotInput{}, false
	}

	now := time.Now().UTC()
	if h.Clock != nil {
		now = h.Clock.Now().UTC()
	}
	return userID, service.SnapshotInput{
		UserID: userID, DaysPerWeek: perfil.DaysPerWeek, SessionMinutes: perfil.SessionMinutes,
		TrainingDays: dias, WeightKg: perfil.CurrentWeightKg, Now: now,
	}, true
}

// paraVistaAdaptacoes traduz as linhas no que a vista desenha — a mesma razão
// de `paraVista`: o `view` não pode importar o repositório sem fechar um ciclo.
func paraVistaAdaptacoes(rows []repo.AdaptationRow) []view.AdaptationRowLike {
	out := make([]view.AdaptationRowLike, 0, len(rows))
	for _, r := range rows {
		out = append(out, view.AdaptationRowLike{
			ID: r.ID, Kind: r.Kind, Payload: r.Payload, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

// Pause põe a jornada em pausa.
//
// "Vou estar fora duas semanas" não é o mesmo que desaparecer, e o sistema tem
// de saber a diferença: os dias em pausa saem do denominador da adesão. Sem
// isso, avisar sai mais caro do que não avisar.
func (h Progress) Pause(w http.ResponseWriter, r *http.Request) { h.pausar(w, r, true) }

// Resume retoma.
func (h Progress) Resume(w http.ResponseWriter, r *http.Request) { h.pausar(w, r, false) }

func (h Progress) pausar(w http.ResponseWriter, r *http.Request, pausar bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Service == nil {
		apierr.Write(w, apierr.Internal, "A jornada está indisponível.", "")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		apierr.Write(w, apierr.ValidationFailed, "Jornada desconhecida.", "id")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	// O corpo é opcional: dizer porquê ajuda, exigi-lo não.
	_ = decode(r, &req)

	now := time.Now().UTC()
	if h.Clock != nil {
		now = h.Clock.Now().UTC()
	}

	var mudou bool
	var err error
	if pausar {
		mudou, err = h.Service.Pause(r.Context(), userID, id, req.Reason, now)
	} else {
		mudou, err = h.Service.Resume(r.Context(), userID, id, now)
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível mudar o estado da jornada.")
		return
	}

	// `changed: false` é resposta, não erro: pausar o que já está em pausa é
	// pedir o estado em que já se está.
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"paused": pausar, "changed": mudou,
	})
}
