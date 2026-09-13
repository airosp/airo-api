package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// ProfileReader dá ao handler o que o motor precisa e o pedido não traz.
//
// O peso, a altura e os dias de treino **não vêm no corpo** de `POST /v1/goals`:
// já estão no perfil, e recebê-los outra vez era abrir a porta a duas verdades
// sobre a mesma pessoa.
type ProfileReader interface {
	Profile(ctx contextLike, userID string) (service.CreateGoalInput, error)
}

type contextLike = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}

type Goals struct {
	Service  *service.GoalService
	Profiles ProfileReader
	Reader   GoalReader
}

// GoalReader lê o objetivo em vigor.
//
// Interface e não o repositório: o handler não tem de saber que existe
// Postgres, e é o que permite um teste do transporte sem base de dados.
type GoalReader interface {
	CurrentGoal(ctx context.Context, userID string) (repo.GoalRow, repo.JourneyRow, error)
	TargetsOf(ctx context.Context, journeyID string) ([]repo.TargetRow, error)
}

// Active devolve o objetivo em vigor.
//
// É o que faz o objetivo sobreviver a mudar de telemóvel. Sem isto, o perfil
// voltava e o foco não — e quem reinstalava a app tinha de escolher outra vez
// o que já tinha escolhido.
//
// 404 quando não há: é uma resposta, não um erro. Quem acabou de criar a conta
// ainda não tem objetivo, e dizer-lhe isso é diferente de falhar.
func (h Goals) Active(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Reader == nil {
		apierr.Write(w, apierr.Internal, "Os objetivos estão indisponíveis.", "")
		return
	}

	goal, journey, err := h.Reader.CurrentGoal(r.Context(), userID)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		apierr.Write(w, apierr.NotFound, "Ainda não tens um objetivo.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler o objetivo.")
		return
	}

	targets, err := h.Reader.TargetsOf(r.Context(), journey.ID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os alvos.")
		return
	}

	out := dto.ActiveGoalResponse{
		ID: goal.ID, Type: goal.Type, Horizon: goal.Horizon,
		Direction: goal.Direction, Priority: goal.Priority, Status: goal.Status,
		Journey: dto.ActiveJourney{
			ID: journey.ID, Horizon: journey.Horizon,
			StartDate:  journey.StartDate.Format("2006-01-02"),
			CycleWeeks: journey.CycleWeeks, Status: journey.Status,
		},
		Targets: make([]dto.TargetView, 0, len(targets)),
	}
	// INVARIANTE 14: horizonte aberto ⇒ sem data-alvo. Sai nulo, e não uma data
	// inventada para o campo não ficar vazio.
	if journey.TargetDate != nil {
		d := journey.TargetDate.Format("2006-01-02")
		out.Journey.TargetDate = &d
	}
	for _, t := range targets {
		v := dto.TargetView{
			Metric: t.Metric, Direction: t.Direction,
			Baseline: t.Baseline, Value: t.Value, Unit: t.Unit,
		}
		if t.DueDate != nil {
			d := t.DueDate.Format("2006-01-02")
			v.DueDate = &d
		}
		out.Targets = append(out.Targets, v)
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func (h Goals) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	var req dto.CreateGoalRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	in, err := h.Profiles.Profile(r.Context(), userID)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	}
	in.UserID = userID
	in.Type, in.Horizon = req.Type, req.Horizon
	in.Direction, in.Priority = req.Direction, req.Priority

	start, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data de início inválida.", "startDate")
		return
	}
	in.StartDate = start

	if req.TargetDate != nil {
		target, err := time.Parse("2006-01-02", *req.TargetDate)
		if err != nil {
			apierr.Write(w, apierr.ValidationFailed, "Data-alvo inválida.", "targetDate")
			return
		}
		in.TargetDate = &target
	} else {
		in.TargetDate = nil
	}

	for _, t := range req.Targets {
		if t.Metric == "body_weight" {
			v := t.Value
			in.TargetWeightKg = &v
		}
	}

	out, err := h.Service.Create(r.Context(), in)
	switch {
	case errors.Is(err, service.ErrHorizonMismatch):
		// A mensagem diz o campo **e** a razão: "inválido" obriga a pessoa a
		// adivinhar qual das duas coisas está errada.
		apierr.Write(w, apierr.HorizonMismatch,
			"Uma jornada de estilo de vida não pode ter data-alvo.", "targetDate")
		return
	case errors.Is(err, repo.ErrGoalAlreadyActive):
		apierr.Write(w, apierr.GoalAlreadyActive,
			"Já tens um objetivo activo. Termina-o ou continua a jornada.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível criar o objetivo.")
		return
	}

	apierr.WriteJSON(w, http.StatusCreated, buildCreateResponse(req, out))
}

// Assess corre o motor sem gravar. É o que alimenta a régua do ecrã de criar
// plano: move-se e a avaliação muda, sem nada ficar escrito.
func (h Goals) Assess(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	var req dto.CreateGoalRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	in, err := h.Profiles.Profile(r.Context(), userID)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto.", "")
		return
	}
	in.UserID = userID
	in.Horizon, in.Direction, in.Priority = req.Horizon, req.Direction, req.Priority
	if req.TargetDate != nil {
		if target, err := time.Parse("2006-01-02", *req.TargetDate); err == nil {
			in.TargetDate = &target
		}
	} else {
		in.TargetDate = nil
	}
	for _, t := range req.Targets {
		if t.Metric == "body_weight" {
			v := t.Value
			in.TargetWeightKg = &v
		}
	}

	apierr.WriteJSON(w, http.StatusOK, dto.FromAssessment(h.Service.Assess(in)))
}

func buildCreateResponse(req dto.CreateGoalRequest, out service.CreateGoalResult) dto.CreateGoalResponse {
	resp := dto.CreateGoalResponse{
		Goal:    dto.Goal{ID: out.GoalID, Horizon: req.Horizon, Status: "active"},
		Journey: dto.Journey{ID: out.JourneyID, StartDate: req.StartDate, TargetDate: req.TargetDate},
		Plan: dto.Plan{
			ID: out.PlanID, Frequency: out.Plan.FrequencyPerWeek,
			SessionMinutes: out.Plan.SessionMinutes, Intensity: out.Plan.Intensity,
		},
		Assessment: dto.FromAssessment(out.Assessment),
	}
	// As fases só saem em horizonte fechado. Num horizonte aberto a resposta
	// traz o ciclo — devolver uma lista vazia convidaria a interface a desenhar
	// um espaço em branco onde devia estar outra coisa.
	for _, p := range out.Phases {
		if out.CycleID != nil {
			break
		}
		resp.Journey.Phases = append(resp.Journey.Phases, dto.Phase{
			Kind: string(p.Kind), Title: p.Title, Intent: p.Intent,
			StartDate: p.StartDateISO[:10], EndDate: p.EndDateISO[:10], Weeks: p.Weeks,
		})
	}
	resp.Nutrition.CalorieTarget = out.Strategy.CalorieTarget
	resp.Nutrition.Macros.Protein = out.Strategy.ProteinG
	resp.Nutrition.Macros.Carbs = out.Strategy.CarbsG
	resp.Nutrition.Macros.Fat = out.Strategy.FatG
	return resp
}

// decode recusa campos desconhecidos: um `targetDate` escrito `target_date`
// passaria despercebido e a jornada nascia sem prazo.
func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
