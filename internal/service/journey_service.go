package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/airosp/airo-api/internal/engine/journey"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/view"
)

/*
 * A jornada, lida de onde já estava guardada.
 *
 * ⚠️ O servidor escreve a jornada inteira desde o primeiro dia — objectivo,
 * alvos, fases, planos e o histórico de acontecimentos — e **nunca a devolvia**.
 * O telemóvel montava a sua com `startJourney`, no `AiroContext`, e ficava com
 * ela: duas contas do mesmo número acabavam com duas jornadas diferentes, cada
 * uma com as suas fases e os seus planos.
 *
 * Isto não decide nada de novo. Lê o que foi decidido quando o objectivo foi
 * criado, e acrescenta a única coisa que é sempre derivada: **em que fase se
 * vai agora** — que é o que decide qual o plano em vigor.
 */

type JourneyReadRepo interface {
	CurrentGoal(ctx context.Context, userID string) (repo.GoalRow, repo.JourneyRow, error)
	TargetsOf(ctx context.Context, journeyID string) ([]repo.TargetRow, error)
	PhasesOf(ctx context.Context, journeyID string) ([]repo.PhaseRow, error)
	PlansOf(ctx context.Context, journeyID string) ([]repo.PlanRow, error)
	EventsOf(ctx context.Context, journeyID string, limite int) ([]repo.JourneyEventRow, error)
	IsPaused(ctx context.Context, journeyID string) (bool, *time.Time, error)
}

type JourneyService struct {
	repo JourneyReadRepo
	cfg  journey.Config
}

func NewJourneyService(r JourneyReadRepo, cfg journey.Config) *JourneyService {
	return &JourneyService{repo: r, cfg: cfg}
}

/** Quantos acontecimentos se mandam. É o que se mostra, não é o arquivo. */
const eventosDaJornada = 60

type JourneyBundle struct {
	Goal    repo.GoalRow
	Journey repo.JourneyRow
	Targets []repo.TargetRow
	Phases  []repo.PhaseRow
	Plans   []repo.PlanRow
	Events  []repo.JourneyEventRow

	/** Em que fase se vai agora. Nula num horizonte sem data de fim. */
	CurrentPhaseIndex *int
	Paused            bool
	PausedSince       *time.Time
}

func (s *JourneyService) Bundle(ctx context.Context, userID string, agora time.Time) (JourneyBundle, error) {
	g, j, err := s.repo.CurrentGoal(ctx, userID)
	if errors.Is(err, repo.ErrNotFound) {
		return JourneyBundle{}, ErrSemJornada
	}
	if err != nil {
		return JourneyBundle{}, err
	}

	alvos, err := s.repo.TargetsOf(ctx, j.ID)
	if err != nil {
		return JourneyBundle{}, err
	}
	fases, err := s.repo.PhasesOf(ctx, j.ID)
	if err != nil {
		return JourneyBundle{}, err
	}
	planos, err := s.repo.PlansOf(ctx, j.ID)
	if err != nil {
		return JourneyBundle{}, err
	}
	eventos, err := s.repo.EventsOf(ctx, j.ID, eventosDaJornada)
	if err != nil {
		return JourneyBundle{}, err
	}
	emPausa, desde, err := s.repo.IsPaused(ctx, j.ID)
	if err != nil {
		return JourneyBundle{}, err
	}

	/*
	 * A fase em curso sai das datas gravadas, e não de as recontar.
	 *
	 * As fases foram escritas quando o objectivo nasceu; recalculá-las aqui
	 * seria arranjar uma segunda repartição que podia discordar da que está no
	 * disco — e é a do disco que os planos referenciam.
	 */
	var actual *int
	for i := range fases {
		if !agora.Before(fases[i].StartDate) && !agora.After(fases[i].EndDate) {
			indice := fases[i].Position
			actual = &indice
			break
		}
	}

	return JourneyBundle{
		Goal: g, Journey: j, Targets: alvos, Phases: fases, Plans: planos, Events: eventos,
		CurrentPhaseIndex: actual, Paused: emPausa, PausedSince: desde,
	}, nil
}

/** Para o `view` não precisar de saber de `json.RawMessage` vazio. */
func PayloadOuVazio(p json.RawMessage) json.RawMessage {
	if len(p) == 0 {
		return json.RawMessage("{}")
	}
	return p
}

/*
 * ParaVista traduz o pacote para o que a rota devolve.
 *
 * Vive aqui e não na `view` porque é a `view` que não pode importar o serviço —
 * é ele que a importa. É o mesmo caminho que o pacote da sessão de treino faz.
 */
func (b JourneyBundle) ParaVista() view.Journey {
	out := view.Journey{
		Goal: view.JourneyGoalView{
			ID: b.Goal.ID, Type: b.Goal.Type, Horizon: b.Goal.Horizon,
			Direction: b.Goal.Direction, Priority: b.Goal.Priority, Status: b.Goal.Status,
		},
		Journey: view.JourneyRecordView{
			ID: b.Journey.ID, Horizon: b.Journey.Horizon, Status: b.Journey.Status,
			StartDateISO:      b.Journey.StartDate.UTC().Format(time.RFC3339),
			CurrentPhaseIndex: b.CurrentPhaseIndex,
		},
		Targets: make([]view.JourneyTarget, 0, len(b.Targets)),
		Phases:  make([]view.JourneyPhase, 0, len(b.Phases)),
		Plans:   make([]view.JourneyPlan, 0, len(b.Plans)),
		Events:  make([]view.JourneyEventView, 0, len(b.Events)),
	}
	if b.Journey.TargetDate != nil {
		out.Journey.TargetDateISO = b.Journey.TargetDate.UTC().Format(time.RFC3339)
	}
	// Em pausa é uma coisa que muda o que o ecrã diz: uma adesão baixa porque
	// alguém avisou que ia estar fora não é uma adesão baixa.
	if b.Paused && b.PausedSince != nil {
		out.Journey.PausedSinceISO = b.PausedSince.UTC().Format(time.RFC3339)
	}

	for _, t := range b.Targets {
		alvo := view.JourneyTarget{
			Metric: t.Metric, Direction: t.Direction,
			Baseline: t.Baseline, Value: t.Value, Unit: t.Unit,
		}
		if t.DueDate != nil {
			alvo.DueDate = t.DueDate.UTC().Format("2006-01-02")
		}
		out.Targets = append(out.Targets, alvo)
	}
	for _, f := range b.Phases {
		titulo, intencao := journey.CopyOf(journey.PhaseKind(f.Kind))
		// Semanas inteiras, arredondadas: uma fase de 20 dias são 3 semanas na
		// frase que a pessoa lê, e ninguém diz "2,86 semanas".
		semanas := int(f.EndDate.Sub(f.StartDate).Hours()/(24*7) + 0.5)
		if semanas < 1 {
			semanas = 1
		}
		out.Phases = append(out.Phases, view.JourneyPhase{
			Kind: f.Kind, Index: f.Position, Title: titulo, Intent: intencao,
			StartDateISO: f.StartDate.UTC().Format(time.RFC3339),
			EndDateISO:   f.EndDate.UTC().Format(time.RFC3339),
			Weeks:        semanas,
		})
	}
	for _, p := range b.Plans {
		out.Plans = append(out.Plans, view.JourneyPlan{
			ID: p.ID, FrequencyPerWeek: p.FrequencyPerWeek, SessionMinutes: p.SessionMinutes,
			Intensity: p.Intensity, Progression: p.Progression, Recovery: p.Recovery,
			EffectiveFromISO: p.EffectiveFrom.UTC().Format("2006-01-02"),
		})
	}
	for _, e := range b.Events {
		out.Events = append(out.Events, view.JourneyEventView{
			Kind: e.Kind, Payload: PayloadOuVazio(e.Payload),
			OccurredAtISO: e.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}
