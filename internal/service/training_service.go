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
	// MaxImpact é o tecto de impacto do perfil. Vazio = sem tecto.
	MaxImpact string
	// Specialist é o treinador da equipa. Vazio = o plano sai como o motor o
	// monta — ver `training/metodo.go`.
	Specialist string
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
		MaxImpact:     training.Impact(in.MaxImpact),
		Specialist:    in.Specialist,
	})
	if err != nil {
		return view.SessionPackage{}, training.Session{}, nil, err
	}
	steps := training.BuildTimeline(s.cfg, session)
	// O rótulo do dia, o treinador e o orçamento de minutos vão com o pacote:
	// eram três contas que os ecrãs refaziam no telemóvel com a mesma entrada.
	pkg := view.BuildSessionPackageCom(s.cfg, session, steps, in.PlanLabel, in.Specialist, minutes)
	return pkg, session, steps, nil
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

	// ClassID, quando a sessão foi uma aula gravada. Numa aula o vídeo lidera:
	// quem decidiu os exercícios e os descansos foi quem a filmou, e o tempo
	// planeado é a duração dela.
	ClassID *string
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
		ClassID:         in.ClassID,
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

// ── Eventos durante a sessão ─────────────────────────────────────────────────

type SessionEvent struct {
	Kind      string
	StepIndex *int
	Payload   map[string]any
	At        time.Time
}

type AppendEventsResult struct {
	Accepted int
	// Duplicates são os que já lá estavam. Reenviar é normal, não é erro: é o
	// que acontece quando a rede cai a meio do envio.
	Duplicates int

	// Status e Streak só vêm preenchidos quando o `session_ended` chegou.
	Status          string
	CountsForStreak bool
	Streak          int
	Ended           bool
}

// AppendEvents recebe o lote e, se a sessão tiver terminado, **decide**.
//
// Os eventos chegam fora de ordem, repetidos e atrasados — é o que acontece a
// quem treina sem rede e sincroniza depois. Nenhum deles conclui nada.
func (s *TrainingService) AppendEvents(ctx context.Context, userID, sessionID string, events []SessionEvent, localDay time.Time) (AppendEventsResult, error) {
	var out AppendEventsResult

	planned, err := s.sessions.PlannedSeconds(ctx, sessionID, userID)
	if err != nil {
		return out, err
	}

	rows := make([]repo.EventRow, 0, len(events))
	for _, e := range events {
		// Um evento sem instante não se pode ordenar nem deduplicar. Recusa-se
		// o evento, não o lote: perder trinta séries por causa de uma é pior.
		if e.At.IsZero() {
			continue
		}
		rows = append(rows, repo.EventRow{
			Kind: e.Kind, StepIndex: e.StepIndex, Payload: e.Payload, OccurredAt: e.At.UTC(),
		})
	}

	err = s.sessions.WithTx(ctx, func(ctx context.Context) error {
		accepted, err := s.sessions.AppendEvents(ctx, sessionID, rows)
		if err != nil {
			return err
		}
		out.Accepted = accepted
		out.Duplicates = len(rows) - accepted

		facts, err := s.sessions.FactsFrom(ctx, sessionID)
		if err != nil {
			return err
		}
		if !facts.Ended {
			return nil
		}
		out.Ended = true

		// A decisão, e as duas coisas que a sustentam: o tempo declarado no
		// `session_ended` e o tempo que os eventos abrangem. Um cliente que
		// declarasse uma hora com todos os eventos dentro de cinco segundos não
		// treinou uma hora.
		duration := facts.DurationSeconds
		if facts.SpanSeconds > 0 && facts.SpanSeconds < duration {
			duration = facts.SpanSeconds
		}

		status := "skipped"
		if planned > 0 && float64(duration) >= float64(planned)*CompletionRatio {
			status = "completed"
		}
		if err := s.sessions.UpdateOutcome(ctx, sessionID, status, duration, facts.SetsCompleted); err != nil {
			return err
		}
		out.Status = status
		out.CountsForStreak = status == "completed"
		return nil
	})
	if err != nil {
		return out, err
	}

	if out.Ended {
		streak, err := s.sessions.Streak(ctx, userID, localDay)
		if err != nil {
			return out, err
		}
		out.Streak = streak
	}
	return out, nil
}

// Open abre a sessão para os eventos terem onde aterrar.
func (s *TrainingService) Open(ctx context.Context, userID string, in TodayInput) (string, error) {
	_, session, steps, err := s.Today(in)
	if err != nil {
		return "", err
	}
	return s.sessions.OpenSession(ctx, repo.SessionRow{
		UserID: userID, Title: session.Title, Focus: string(session.Focus),
		OccurredAt: s.clk.Now(), LocalDay: in.LocalDay,
		PlannedSeconds: training.RemainingSeconds(s.cfg, steps, 0),
		SetsPlanned:    training.TotalSets(steps),
		Kcal:           session.EstimatedKcal,
	}, PrescriptionsFrom(session))
}
