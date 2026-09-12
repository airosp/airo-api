package handlers

import (
	"errors"
	"net/http"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// TrainingProfileReader dá ao handler o que o motor precisa e o pedido não traz.
//
// O rótulo do dia, o equipamento e os exercícios fixados vêm do plano e do
// perfil. Recebê-los no corpo era deixar o cliente escolher o treino — e o
// treino é uma decisão.
type TrainingProfileReader interface {
	TrainingProfile(ctx contextLike, userID string, day time.Time) (service.TodayInput, error)
}

type Training struct {
	Service  *service.TrainingService
	Profiles TrainingProfileReader
}

func (h Training) Today(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	day, err := localDay(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}

	in, err := h.Profiles.TrainingProfile(r.Context(), userID, day)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	}
	in.LocalDay = day

	pkg, _, _, err := h.Service.Today(in)
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível montar o treino de hoje.", "")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, pkg)
}

func (h Training) Record(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	var req dto.RecordSessionRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	occurred, err := time.Parse(time.RFC3339, req.OccurredAt)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Instante inválido.", "occurredAt")
		return
	}
	day, err := time.Parse("2006-01-02", req.LocalDay)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}
	if req.PlannedSeconds <= 0 {
		apierr.Write(w, apierr.ValidationFailed, "A sessão tem de dizer quanto tempo pedia.", "plannedSeconds")
		return
	}
	if req.DurationSeconds < 0 {
		apierr.Write(w, apierr.ValidationFailed, "A duração não pode ser negativa.", "durationSeconds")
		return
	}

	// A sessão é remontada aqui, a partir do plano — não vem no corpo. É o que
	// impede o cliente de gravar um treino que a Airo nunca propôs.
	profile, err := h.Profiles.TrainingProfile(r.Context(), userID, day)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto.", "")
		return
	}
	profile.LocalDay = day
	_, session, steps, err := h.Service.Today(profile)
	if err != nil {
		apierr.Write(w, apierr.Internal, "Não foi possível remontar a sessão.", "")
		return
	}

	in := service.RecordSessionInput{
		UserID:          userID,
		IdempotencyKey:  r.Header.Get("Idempotency-Key"),
		Title:           session.Title,
		Focus:           string(session.Focus),
		OccurredAt:      occurred,
		LocalDay:        day,
		PlannedSeconds:  req.PlannedSeconds,
		DurationSeconds: req.DurationSeconds,
		SetsPlanned:     countMainSets(steps),
		SetsDone:        req.SetsDone,
		Kcal:            session.EstimatedKcal,
		Prescriptions:   service.PrescriptionsFrom(session),
	}
	if req.Blocks != nil {
		in.WarmupSeconds, in.MainSeconds, in.CooldownSeconds =
			req.Blocks.WarmupSeconds, req.Blocks.MainSeconds, req.Blocks.CooldownSeconds
	}

	out, err := h.Service.Record(r.Context(), in)
	switch {
	case errors.Is(err, repo.ErrDuplicateSession):
		// Idempotência: 200 com o registo existente, não 409. Reenviar não é um
		// conflito — é a rede a voltar.
		apierr.WriteJSON(w, http.StatusOK, dto.RecordSessionResponse{
			ID: out.ID, Status: out.Status, CountsForStreak: out.CountsForStreak, Streak: out.Streak,
		})
		return
	case err != nil:
		apierr.Write(w, apierr.Internal, "Não foi possível gravar o treino.", "")
		return
	}

	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	apierr.WriteJSON(w, status, dto.RecordSessionResponse{
		ID: out.ID, Status: out.Status, CountsForStreak: out.CountsForStreak, Streak: out.Streak,
	})
}

// localDay lê o dia do utilizador do pedido. Sem ele, o servidor usaria o seu
// próprio dia — e o dia do servidor não é o de ninguém.
func localDay(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("localDay")
	if raw == "" {
		return time.Now().UTC().Truncate(24 * time.Hour), nil
	}
	return time.Parse("2006-01-02", raw)
}
