package handlers

import (
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
