// Package service orquestra: lê → chama o motor → escreve.
//
// É o único que sabe que existe base de dados. O motor não sabe, e é isso que o
// torna testável sem infra-estrutura.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

type CreateGoalInput struct {
	UserID string

	Type      string
	Horizon   string
	Direction string
	Priority  string

	StartDate  time.Time
	TargetDate *time.Time

	// Body e Training alimentam o motor. Vêm do perfil, não do pedido — o
	// cliente não manda o peso no corpo de `POST /v1/goals`.
	CurrentWeightKg float64
	TargetWeightKg  *float64
	HeightCm        *float64
	Age             *int
	Sex             *string

	DaysPerWeek    int
	SessionMinutes int
	Experience     string

	// Nutrição
	NutritionGoal string
	MealsPerDay   int
}

type CreateGoalResult struct {
	GoalID    string
	JourneyID string
	PlanID    string
	// Plan são os números que a fase produziu. O id sozinho não chega: o ecrã
	// mostra "2× por semana, 38 min" e não um uuid.
	Plan repo.PlanRow
	// Assessment é o que a app precisa para escolher o tom. **Sem o score.**
	Assessment goal.Assessment
	Phases     []journey.Phase
	CycleID    *string
	Strategy   repo.StrategyRow
}

type Configs struct {
	Goal      goal.Config
	Journey   journey.Config
	Nutrition nutrition.Config
}

type GoalService struct {
	tx    *repo.TxManager
	goals *repo.GoalRepo
	cfg   Configs
	clk   clock.Clock
}

func NewGoalService(tx *repo.TxManager, goals *repo.GoalRepo, cfg Configs, clk clock.Clock) *GoalService {
	return &GoalService{tx: tx, goals: goals, cfg: cfg, clk: clk}
}

// Assess corre o motor **sem gravar**. É o que alimenta a régua do ecrã de criar
// plano: move-se e a avaliação muda, sem nada ficar escrito.
func (s *GoalService) Assess(in CreateGoalInput) goal.Assessment {
	return goal.Assess(s.cfg.Goal, s.engineInput(in))
}

func (s *GoalService) engineInput(in CreateGoalInput) goal.Input {
	gi := goal.GoalInput{TargetWeightKg: in.TargetWeightKg}
	if in.TargetDate != nil {
		gi.TargetDateISO = in.TargetDate.Format("2006-01-02")
	}
	if in.Priority != "" {
		p := goal.Priority(in.Priority)
		gi.Priority = &p
	}
	body := goal.Body{CurrentWeightKg: in.CurrentWeightKg, HeightCm: in.HeightCm, Age: in.Age}
	if in.Sex != nil {
		sx := goal.Sex(*in.Sex)
		body.Sex = &sx
	}
	return goal.Input{
		Body:     body,
		Goal:     gi,
		Training: goal.Training{DaysPerWeek: in.DaysPerWeek, DurationMinutes: in.SessionMinutes},
		NowISO:   s.clk.Now().UTC().Format(time.RFC3339),
	}
}

// Create é a operação mais pesada da API: corre o Goal Engine, cria a jornada,
// reparte as fases (ou abre o primeiro ciclo), gera o plano e a estratégia
// alimentar.
//
// **Uma transação.** Um objectivo sem jornada, ou uma jornada sem plano, é pior
// do que nenhum objectivo: a app abriria num estado que nenhum ecrã sabe
// desenhar.
func (s *GoalService) Create(ctx context.Context, in CreateGoalInput) (CreateGoalResult, error) {
	// A validação do horizonte é feita aqui e **também** pelo `CHECK` do
	// esquema. Aqui para dar um erro que se possa explicar; lá para garantir que
	// nenhum caminho a contorna.
	if in.Horizon == "fixed" && in.TargetDate == nil {
		return CreateGoalResult{}, fmt.Errorf("%w: horizonte fechado exige data-alvo", ErrHorizonMismatch)
	}
	if in.Horizon != "fixed" && in.TargetDate != nil {
		return CreateGoalResult{}, fmt.Errorf("%w: uma jornada de estilo de vida não pode ter data-alvo", ErrHorizonMismatch)
	}

	assessment := s.Assess(in)
	var out CreateGoalResult
	out.Assessment = assessment

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		goalID, err := s.goals.InsertGoal(ctx, repo.GoalRow{
			UserID: in.UserID, Type: in.Type, Horizon: in.Horizon,
			Direction: in.Direction, Priority: in.Priority, Status: "active",
		})
		if err != nil {
			return err
		}
		out.GoalID = goalID

		jr := repo.JourneyRow{
			GoalID: goalID, Horizon: in.Horizon, StartDate: in.StartDate,
			TargetDate: in.TargetDate, Status: "active",
		}
		if in.Horizon != "fixed" {
			// Sem prazo, a jornada corre em ciclos. O esquema exige-o, e com
			// razão: uma jornada sem fim e sem ciclo não tem onde ser revista.
			weeks := s.cfg.Journey.OpenEndedCycleWeeks
			jr.CycleWeeks = &weeks
		}
		journeyID, err := s.goals.InsertJourney(ctx, jr)
		if err != nil {
			return err
		}
		out.JourneyID = journeyID

		// ── fases ou ciclo ───────────────────────────────────────────────────
		ej := journey.Journey{ID: journeyID, GoalID: goalID, StartDateISO: in.StartDate.Format(time.RFC3339)}
		if in.TargetDate != nil {
			ej.TargetDateISO = in.TargetDate.Format(time.RFC3339)
		}
		phases := journey.BuildPhases(s.cfg.Journey, ej)
		out.Phases = phases

		var phaseID, cycleID *string
		if in.Horizon == "fixed" {
			rows := make([]repo.PhaseRow, 0, len(phases))
			for _, p := range phases {
				start, _ := time.Parse(time.RFC3339, p.StartDateISO)
				end, _ := time.Parse(time.RFC3339, p.EndDateISO)
				rows = append(rows, repo.PhaseRow{
					Kind: string(p.Kind), Position: p.Index,
					StartDate: start.UTC(), EndDate: end.UTC(),
				})
			}
			if err := s.goals.InsertPhases(ctx, journeyID, rows); err != nil {
				return err
			}
		} else {
			review := in.StartDate.AddDate(0, 0, s.cfg.Journey.OpenEndedCycleWeeks*7)
			id, err := s.goals.InsertCycle(ctx, journeyID, 1, in.StartDate, review)
			if err != nil {
				return err
			}
			cycleID = &id
			out.CycleID = &id
		}

		// ── alvo ─────────────────────────────────────────────────────────────
		if in.TargetWeightKg != nil {
			direction := "decrease"
			if *in.TargetWeightKg > in.CurrentWeightKg {
				direction = "increase"
			} else if *in.TargetWeightKg == in.CurrentWeightKg {
				direction = "maintain"
			}
			var due *time.Time
			if in.TargetDate != nil {
				due = in.TargetDate
			}
			if err := s.goals.InsertTargets(ctx, journeyID, []repo.TargetRow{{
				Metric: "body_weight", Direction: direction,
				Baseline: in.CurrentWeightKg, Value: *in.TargetWeightKg, Unit: "kg", DueDate: due,
			}}); err != nil {
				return err
			}
		}

		// ── plano da primeira fase ───────────────────────────────────────────
		//
		// A fase não é uma etiqueta: traduz-se em números. Adaptação é menos
		// frequência, sessões mais curtas e mais recuperação.
		kind := journey.Adaptation
		if len(phases) > 0 {
			kind = phases[0].Kind
		}
		base := journey.Plan{Frequency: in.DaysPerWeek, SessionDurationMinutes: in.SessionMinutes}
		adjusted, phasePlan := journey.ApplyPhaseToPlan(s.cfg.Journey, kind, base)

		planID, err := s.goals.InsertPlan(ctx, journeyID, phaseID, cycleID, repo.PlanRow{
			FrequencyPerWeek: adjusted.Frequency,
			SessionMinutes:   adjusted.SessionDurationMinutes,
			Intensity:        string(phasePlan.Intensity),
			Progression:      string(phasePlan.Progression),
			Recovery:         string(phasePlan.Recovery),
			EffectiveFrom:    in.StartDate,
		})
		if err != nil {
			return err
		}
		out.PlanID = planID
		out.Plan = repo.PlanRow{
			ID:               planID,
			FrequencyPerWeek: adjusted.Frequency,
			SessionMinutes:   adjusted.SessionDurationMinutes,
			Intensity:        string(phasePlan.Intensity),
			Progression:      string(phasePlan.Progression),
			Recovery:         string(phasePlan.Recovery),
			EffectiveFrom:    in.StartDate,
		}

		// ── estratégia alimentar ─────────────────────────────────────────────
		strategy, err := s.buildStrategy(in, assessment)
		if err != nil {
			return err
		}
		if _, err := s.goals.InsertStrategy(ctx, in.UserID, journeyID, strategy, in.StartDate); err != nil {
			return err
		}
		out.Strategy = strategy

		// ── histórico ────────────────────────────────────────────────────────
		for _, ev := range []struct {
			kind    string
			payload any
		}{
			{"goal.created", map[string]any{"type": in.Type, "horizon": in.Horizon, "direction": in.Direction}},
			{"journey.started", map[string]any{"phases": len(phases)}},
			{"plan.generated", map[string]any{"frequency": adjusted.Frequency, "minutes": adjusted.SessionDurationMinutes}},
		} {
			if err := s.goals.AppendEvent(ctx, journeyID, ev.kind, ev.payload); err != nil {
				return err
			}
		}

		// A avaliação guarda a **versão da configuração**: sem ela, uma
		// avaliação de há três meses deixa de ser explicável quando os limiares
		// mudarem.
		return s.goals.InsertAssessment(ctx, journeyID, map[string]any{
			"configVersion": s.cfg.Goal.Version,
			"status":        assessment.Status,
			"metrics":       assessment.Metrics,
			"signals":       assessment.Signals,
		}, 0, string(assessment.Metrics.Body.Confidence))
	})
	if err != nil {
		return CreateGoalResult{}, err
	}
	return out, nil
}

func (s *GoalService) buildStrategy(in CreateGoalInput, a goal.Assessment) (repo.StrategyRow, error) {
	tdee := 0
	if a.Metrics.Energy.TdeeKcal != nil {
		tdee = a.Metrics.Energy.TdeeKcal.Estimate
	}
	if tdee <= 0 {
		// Sem altura ou idade não há TDEE. Em vez de inventar um, usa-se o piso
		// — e o ecrã diz que a estimativa melhora com esses dados.
		tdee = s.cfg.Nutrition.MinDailyCalories
	}

	goalType := nutrition.GoalType(in.NutritionGoal)
	if goalType == "" {
		goalType = nutrition.Maintain
	}
	target := nutrition.ComputeCalorieTarget(s.cfg.Nutrition, nutrition.CalorieTargetInput{
		TDEEKcal: float64(tdee), GoalType: goalType,
	})
	macros := nutrition.DistributeMacros(s.cfg.Nutrition, target.Target, in.CurrentWeightKg, goalType)

	return repo.StrategyRow{
		Goal: string(goalType), CalorieTarget: target.Target,
		ProteinG: macros.Protein, CarbsG: macros.Carbs, FatG: macros.Fat,
		TDEEEstimated: tdee,
	}, nil
}
