package service

import (
	"context"
	"time"

	"github.com/airosp/airo-api/internal/domain"
	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/view"
)

type TrainingService struct {
	sessions *repo.SessionRepo
	cfg      training.Config
	clk      clock.Clock
}

func NewTrainingService(sessions *repo.SessionRepo, cfg training.Config, clk clock.Clock) *TrainingService {
	return &TrainingService{sessions: sessions, cfg: cfg, clk: clk}
}

type TodayInput struct {
	PlanLabel      string
	Experience     string
	Equipment      []string
	WorkoutMinutes int
	LocalDay       time.Time

	Pinned        []string
	Excluded      []string
	Prescriptions map[string]training.Prescription
}

// Today monta a sessão do dia e devolve o **pacote** — não os exercícios.
//
// A diferença é a fronteira: o cliente recebe cada passo já decidido e
// percorre-o. Se recebesse os exercícios, teria de montar a linha do tempo, e
// aí estaria a decidir.
func (s *TrainingService) Today(in TodayInput) (view.SessionPackage, training.Session, []training.Step, error) {
	minutes := s.cfg.PlanDayMinutes(in.PlanLabel, in.WorkoutMinutes)

	session, err := training.BuildSession(s.cfg, training.BuildInput{
		PlanLabel: in.PlanLabel, Experience: training.Experience(in.Experience),
		Equipment: in.Equipment, Minutes: minutes,
		DayISO:        in.LocalDay.Format("2006-01-02"),
		Pinned:        in.Pinned,
		Excluded:      in.Excluded,
		Prescriptions: in.Prescriptions,
	})
	if err != nil {
		return view.SessionPackage{}, training.Session{}, nil, err
	}
	steps := training.BuildTimeline(s.cfg, session)
	return view.BuildSessionPackage(s.cfg, session, steps), session, steps, nil
}

// RecordSessionInput é o que o cliente envia.
//
// ⚠️ **Sem `status`.** Quem decide se a sessão conta como feita é o servidor.
// Deixar o cliente mandar `"completed"` devolvia-lhe a regra pela porta das
// traseiras — e a regra é o que distingue treinar de folhear o ecrã.
type RecordSessionInput struct {
	UserID         string
	IdempotencyKey string

	Title string
	Focus string

	OccurredAt time.Time
	// LocalDay é o dia **do utilizador**, e não `date(OccurredAt)`: um treino à
	// uma da manhã em Maputo é dia anterior em UTC.
	LocalDay time.Time

	PlannedSeconds  int
	DurationSeconds int

	SetsPlanned int
	SetsDone    int
	Kcal        int

	WarmupSeconds   *int
	MainSeconds     *int
	CooldownSeconds *int

	Prescriptions []repo.PrescriptionRow
}

type RecordSessionResult struct {
	ID     string
	Status string
	// CountsForStreak é a decisão, não o dado: o cliente desenha a sequência
	// que lhe mandam.
	CountsForStreak bool
	Streak          int
	Replayed        bool
}

// CompletionRatio — abaixo desta fracção do tempo pedido, a sessão não conta.
//
// Metade é generoso de propósito: quem treina depressa, ou corta séries a meio,
// continua a contar. Quem passou os passos à frente em cinco segundos não.
const CompletionRatio = 0.5

// Record grava a sessão e **decide** o que ela foi.
func (s *TrainingService) Record(ctx context.Context, in RecordSessionInput) (RecordSessionResult, error) {
	if in.IdempotencyKey != "" {
		existing, found, err := s.sessions.FindByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey)
		if err != nil {
			return RecordSessionResult{}, err
		}
		if found {
			streak, err := s.sessions.Streak(ctx, in.UserID, in.LocalDay)
			if err != nil {
				return RecordSessionResult{}, err
			}
			return RecordSessionResult{
				ID: existing.ID, Status: existing.Status,
				CountsForStreak: existing.Status == "completed",
				Streak:          streak, Replayed: true,
			}, nil
		}
	}

	// A decisão. Uma linha, e é o centro de tudo.
	status := "skipped"
	if in.PlannedSeconds > 0 && float64(in.DurationSeconds) >= float64(in.PlannedSeconds)*CompletionRatio {
		status = "completed"
	}

	row := repo.SessionRow{
		UserID: in.UserID, Title: in.Title, Focus: in.Focus, Status: status,
		OccurredAt: in.OccurredAt, LocalDay: in.LocalDay,
		PlannedSeconds: in.PlannedSeconds, DurationSeconds: in.DurationSeconds,
		SetsPlanned: in.SetsPlanned, SetsDone: in.SetsDone, Kcal: in.Kcal,
		ExerciseCount:   len(in.Prescriptions),
		WarmupSeconds:   in.WarmupSeconds,
		MainSeconds:     in.MainSeconds,
		CooldownSeconds: in.CooldownSeconds,
	}
	if in.IdempotencyKey != "" {
		key := in.IdempotencyKey
		row.IdempotencyKey = &key
	}

	// Uma sessão saltada grava **na mesma** — invariante 7. A adesão precisa de
	// distinguir quem abriu e desistiu de quem nunca apareceu.
	id, err := s.sessions.Insert(ctx, row, in.Prescriptions)
	if err != nil {
		return RecordSessionResult{}, err
	}

	streak, err := s.sessions.Streak(ctx, in.UserID, in.LocalDay)
	if err != nil {
		return RecordSessionResult{}, err
	}

	return RecordSessionResult{
		ID: id, Status: status, CountsForStreak: status == "completed", Streak: streak,
	}, nil
}

// PrescriptionsFrom traduz a sessão montada em prescrições para gravar.
//
// Existe para que o cliente não tenha de reenviar o que o servidor já sabe: ele
// manda o que aconteceu, não o que estava planeado.
func PrescriptionsFrom(session training.Session) []repo.PrescriptionRow {
	out := make([]repo.PrescriptionRow, 0, len(session.Exercises))
	for i, e := range session.Exercises {
		target := domain.Reps(e.Target)
		if e.Exercise.Measure == training.Time {
			target = domain.Seconds(e.Target)
		}
		p := repo.PrescriptionRow{
			ExerciseSlug: e.Exercise.ID, Position: i, Role: string(e.Role),
			Sets: e.Sets, Target: target, RestSeconds: e.RestSeconds,
		}
		for k := 0; k < e.Sets; k++ {
			p.Detail = append(p.Detail, repo.SetRow{Index: k, Target: target})
		}
		out = append(out, p)
	}
	return out
}
