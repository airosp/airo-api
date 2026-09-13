package handlers

import (
	"context"
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

// SessionHistory lê o histórico de treinos.
//
// Interface e não o repositório: o handler não tem de saber que existe
// Postgres, e um teste do transporte não tem de levantar uma base de dados
// para provar que o intervalo é validado.
type SessionHistory interface {
	History(ctx context.Context, userID string, from, to time.Time) ([]repo.HistoryRow, error)
}

type Training struct {
	Service  *service.TrainingService
	Profiles TrainingProfileReader
	Sessions SessionHistory
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
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	case err != nil:
		// Qualquer outro erro aqui é nosso. Dizer "perfil incompleto" a uma
		// coluna em falta mandou-me à procura no sítio errado durante uma
		// hora: o cliente lia uma instrução e o registo não dizia nada.
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return
	}
	in.LocalDay = day

	pkg, _, _, err := h.Service.Today(in)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível montar o treino de hoje.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, pkg)
}

// History devolve as sessões de um intervalo de dias.
//
// É o que faz o histórico sobreviver a mudar de telemóvel. O `id` que volta é a
// chave de idempotência com que a sessão foi gravada — a mesma que o aparelho
// deu —, e é assim que ele reconhece o que já é seu em vez de duplicar.
func (h Training) History(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Sessions == nil {
		apierr.Write(w, apierr.Internal, "O histórico está indisponível.", "")
		return
	}

	from, err := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data inicial inválida.", "from")
		return
	}
	to, err := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data final inválida.", "to")
		return
	}
	if to.Before(from) {
		apierr.Write(w, apierr.ValidationFailed, "O fim é antes do início.", "to")
		return
	}
	// Como no diário: sem limite, o pedido fica cada vez mais lento à medida
	// que a pessoa treina.
	if to.Sub(from) > 366*24*time.Hour {
		apierr.Write(w, apierr.ValidationFailed, "Pede no máximo um ano de cada vez.", "to")
		return
	}

	rows, err := h.Sessions.History(r.Context(), userID, from, to)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o histórico.")
		return
	}

	out := dto.SessionHistoryResponse{Sessions: make([]dto.SessionHistoryItem, 0, len(rows))}
	for _, s := range rows {
		item := dto.SessionHistoryItem{
			ID: s.ID, Title: s.Title, Focus: s.Focus, Status: s.Status,
			OccurredAt:     s.OccurredAt.UTC().Format(time.RFC3339),
			LocalDay:       s.LocalDay.Format("2006-01-02"),
			PlannedSeconds: s.PlannedSeconds, DurationSeconds: s.DurationSeconds,
			SetsPlanned: s.SetsPlanned, SetsDone: s.SetsDone,
			Kcal: s.Kcal, Exercises: s.ExerciseCount,
		}
		// A chave do telemóvel ganha ao identificador do servidor: é ela que o
		// aparelho conhece, e é por ela que reconhece o que já gravou.
		if s.IdempotencyKey != nil && *s.IdempotencyKey != "" {
			item.ID = *s.IdempotencyKey
		}
		if s.WarmupSeconds != nil || s.MainSeconds != nil || s.CooldownSeconds != nil {
			item.Blocks = &dto.SessionBlocks{
				WarmupSeconds:   valorOuZero(s.WarmupSeconds),
				MainSeconds:     valorOuZero(s.MainSeconds),
				CooldownSeconds: valorOuZero(s.CooldownSeconds),
			}
		}
		out.Sessions = append(out.Sessions, item)
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func valorOuZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
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
		apierr.WriteInternal(w, r, err, "Não foi possível remontar a sessão.")
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
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o treino.")
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
