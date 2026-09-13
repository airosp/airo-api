package service

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/airosp/airo-api/internal/engine/journey"
	"github.com/airosp/airo-api/internal/engine/portable"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// ProgressReader lê a história de que o progresso vive.
type ProgressReader interface {
	Measurements(ctx context.Context, userID, metric string, since time.Time) ([]repo.MeasurementRow, error)
	SessionsSince(ctx context.Context, userID string, since time.Time) ([]repo.HistoryRow, error)
}

// JourneyReader dá a jornada em vigor.
type JourneyReader interface {
	CurrentGoal(ctx context.Context, userID string) (repo.GoalRow, repo.JourneyRow, error)
	TargetsOf(ctx context.Context, journeyID string) ([]repo.TargetRow, error)
}

type ProgressService struct {
	progress    ProgressReader
	journeys    JourneyReader
	adaptations AdaptationStore
	assessments AssessmentWriter
	plans       PlanWriter
	cfg         journey.Config
}

func NewProgressService(p ProgressReader, j JourneyReader, cfg journey.Config) *ProgressService {
	return &ProgressService{progress: p, journeys: j, cfg: cfg}
}

// ErrSemJornada — ainda não há objectivo, logo não há progresso a medir.
var ErrSemJornada = errors.New("sem jornada activa")

// Snapshot é o retrato do progresso.
//
// ⚠️ **Duas formas, conforme o horizonte.** Num horizonte fechado há uma data a
// prever; num aberto não há, e devolver `forecast: null` convidaria a interface
// a mostrar um espaço vazio onde devia estar consistência. Sucesso em modo
// contínuo é consistência × progressão × recuperação, não aproximação a um
// número.
type Snapshot struct {
	Horizon string

	// Horizonte fechado.
	Trend     *journey.Trend
	Adherence journey.Adherence
	Forecast  *Forecast
	Risks     []journey.Risk

	// Horizonte aberto.
	Cycle       *CycleView
	Consistency *Consistency
	Progression *Progression

	// Comum às duas.
	MetricKey    string
	WeeksElapsed int
}

type Forecast struct {
	ExpectedOn *time.Time
	Confidence string
}

type CycleView struct {
	Index      int
	ReviewDate time.Time
}

type Consistency struct {
	SessionsPlanned int
	SessionsDone    int
	Rate            float64
}

type Progression struct {
	// VolumeTrend: up | flat | down.
	VolumeTrend     string
	RecentMinutes   float64
	BaselineMinutes float64
}

type SnapshotInput struct {
	UserID string
	// Do perfil: é o plano contra o qual se mede a adesão.
	DaysPerWeek    int
	SessionMinutes int
	TrainingDays   []int
	WeightKg       float64
	Now            time.Time
}

// Snapshot monta o retrato. Nenhuma decisão é tomada aqui: é o motor que decide,
// e este método é quem lhe leva os factos.
func (s *ProgressService) Snapshot(ctx context.Context, in SnapshotInput) (Snapshot, error) {
	_, j, err := s.journeys.CurrentGoal(ctx, in.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return Snapshot{}, ErrSemJornada
	}
	if err != nil {
		return Snapshot{}, err
	}

	sessoes, err := s.progress.SessionsSince(ctx, in.UserID, j.StartDate)
	if err != nil {
		return Snapshot{}, err
	}
	medicoes, err := s.progress.Measurements(ctx, in.UserID, "body_weight", j.StartDate)
	if err != nil {
		return Snapshot{}, err
	}

	registos := make([]journey.SessionRecord, 0, len(sessoes))
	for _, h := range sessoes {
		actual := int(portable.RoundJS(float64(h.DurationSeconds) / 60))
		registos = append(registos, journey.SessionRecord{
			OccurredAtISO:          h.OccurredAt.UTC().Format(time.RFC3339),
			Status:                 h.Status,
			PlannedDurationMinutes: int(portable.RoundJS(float64(h.PlannedSeconds) / 60)),
			ActualDurationMinutes:  &actual,
		})
	}

	plano := journey.Plan{
		Frequency:              in.DaysPerWeek,
		SessionDurationMinutes: in.SessionMinutes,
		TrainingDays:           in.TrainingDays,
	}
	adesao := journey.ComputeAdherence(s.cfg, journey.AdherenceInput{
		Sessions:       registos,
		Plan:           plano,
		PeriodStartISO: j.StartDate.UTC().Format(time.RFC3339),
		PeriodEndISO:   in.Now.UTC().Format(time.RFC3339),
	})

	leituras := make([]journey.Measurement, 0, len(medicoes))
	for _, m := range medicoes {
		leituras = append(leituras, journey.Measurement{
			Metric: m.Metric, Value: m.Value, RecordedAt: m.RecordedAt,
		})
	}
	tendencia := journey.ComputeTrend(s.cfg, "body_weight", leituras, in.Now)

	semanas := journey.WeeksSince(j.StartDate.UTC().Format(time.RFC3339), in.Now.UTC().Format(time.RFC3339))
	minutos := minutosExecutados(sessoes, in.Now)

	out := Snapshot{
		Horizon:      j.Horizon,
		Adherence:    adesao,
		Trend:        tendencia,
		MetricKey:    "body_weight",
		WeeksElapsed: semanas,
	}

	alvo, movingTowards, atingido := s.alvoDe(ctx, j.ID, tendencia)

	if j.Horizon == "open_ended" {
		// Sem data a prever: o que se mede é consistência e progressão.
		out.Consistency = &Consistency{
			SessionsPlanned: adesao.PlannedSessions,
			SessionsDone:    adesao.CompletedSessions,
			Rate:            razao(adesao.CompletedSessions, adesao.PlannedSessions),
		}
		out.Progression = progressaoDe(minutos)
		out.Cycle = cicloDe(j, in.Now, s.cfg)
	} else {
		out.Forecast = previsaoDe(tendencia, alvo)
	}

	out.Risks = journey.DetectRisks(s.cfg, journey.RiskInput{
		Adherence:           adesao,
		Trend:               tendencia,
		BodyWeightKg:        in.WeightKg,
		WeeksElapsed:        semanas,
		ExecutedMinutes:     minutos,
		MovingTowardsTarget: movingTowards,
		TargetReached:       atingido,
		NowISO:              in.Now.UTC().Format(time.RFC3339),
	})
	return out, nil
}

// alvoDe devolve o alvo de peso e se a tendência caminha para ele.
//
// `movingTowards` é nil quando não se sabe — e nil é diferente de `false`. É a
// diferença entre "não responde ao plano" e "ainda é cedo para dizer".
func (s *ProgressService) alvoDe(ctx context.Context, journeyID string, t *journey.Trend) (*float64, *bool, bool) {
	targets, err := s.journeys.TargetsOf(ctx, journeyID)
	if err != nil {
		return nil, nil, false
	}
	for _, target := range targets {
		if target.Metric != "body_weight" {
			continue
		}
		alvo := target.Value
		if t == nil || t.RatePerWeek == nil {
			return &alvo, nil, false
		}
		distancia := alvo - t.RollingAverage
		atingido := math.Abs(distancia) < 0.5
		// Caminha para o alvo quando o sinal do ritmo é o da distância.
		anda := portable.Sign(*t.RatePerWeek) == portable.Sign(distancia) && *t.RatePerWeek != 0
		return &alvo, &anda, atingido
	}
	return nil, nil, false
}

func previsaoDe(t *journey.Trend, alvo *float64) *Forecast {
	if t == nil || t.RatePerWeek == nil || alvo == nil || *t.RatePerWeek == 0 {
		return nil
	}
	semanas := (*alvo - t.RollingAverage) / *t.RatePerWeek
	// Um ritmo que afasta do alvo dá semanas negativas: não há data a prever, e
	// inventar uma seria pior do que dizer que não se sabe.
	if semanas <= 0 || math.IsInf(semanas, 0) || math.IsNaN(semanas) {
		return &Forecast{Confidence: string(t.Confidence)}
	}
	quando := time.Now().UTC().AddDate(0, 0, int(portable.RoundJS(semanas*7)))
	return &Forecast{ExpectedOn: &quando, Confidence: string(t.Confidence)}
}

func cicloDe(j repo.JourneyRow, now time.Time, c journey.Config) *CycleView {
	semanas := c.OpenEndedCycleWeeks
	if j.CycleWeeks != nil && *j.CycleWeeks > 0 {
		semanas = *j.CycleWeeks
	}
	if semanas <= 0 {
		return nil
	}
	dias := int(now.Sub(j.StartDate).Hours() / 24)
	index := dias/(semanas*7) + 1
	inicio := j.StartDate.AddDate(0, 0, (index-1)*semanas*7)
	return &CycleView{Index: index, ReviewDate: inicio.AddDate(0, 0, semanas*7)}
}

// minutosExecutados separa a semana mais recente das anteriores.
//
// É o que permite ver um salto de volume: o piso dos 60 minutos está no motor,
// porque retomar de um volume baixo não é excesso de carga.
func minutosExecutados(sessoes []repo.HistoryRow, now time.Time) *journey.ExecutedMinutes {
	if len(sessoes) == 0 {
		return nil
	}
	corte := now.AddDate(0, 0, -7)
	var recente, antes float64
	var semanasAntes float64

	maisAntiga := sessoes[0].OccurredAt
	for _, h := range sessoes {
		minutos := float64(h.DurationSeconds) / 60
		if h.OccurredAt.After(corte) {
			recente += minutos
		} else {
			antes += minutos
		}
	}
	if d := corte.Sub(maisAntiga).Hours() / (24 * 7); d > 0 {
		semanasAntes = d
	}
	if semanasAntes < 1 {
		// Sem histórico anterior não há base de comparação, e um salto medido
		// contra meia semana é ruído.
		return nil
	}
	return &journey.ExecutedMinutes{Recent: recente, Baseline: antes / semanasAntes}
}

func progressaoDe(m *journey.ExecutedMinutes) *Progression {
	if m == nil || m.Baseline <= 0 {
		return &Progression{VolumeTrend: "flat"}
	}
	delta := (m.Recent - m.Baseline) / m.Baseline
	trend := "flat"
	switch {
	case delta > 0.05:
		trend = "up"
	case delta < -0.05:
		trend = "down"
	}
	return &Progression{VolumeTrend: trend, RecentMinutes: m.Recent, BaselineMinutes: m.Baseline}
}

func razao(feito, planeado int) float64 {
	if planeado <= 0 {
		return 0
	}
	return portable.RoundTo(float64(feito)/float64(planeado), 3)
}

// ── Adaptações ───────────────────────────────────────────────────────────────

// AdaptationStore guarda e decide as propostas.
type AdaptationStore interface {
	Propose(ctx context.Context, journeyID, assessmentID, kind string, payload any) (string, error)
	Pending(ctx context.Context, journeyID string) ([]repo.AdaptationRow, error)
	HasPending(ctx context.Context, journeyID string) (bool, error)
	Decide(ctx context.Context, userID, id string, aplicar bool) (repo.AdaptationRow, error)
	DismissedRecently(ctx context.Context, journeyID, kind string, desde time.Time) (bool, error)
}

// AssessmentWriter grava a avaliação que originou a proposta.
type AssessmentWriter interface {
	InsertAssessment(ctx context.Context, journeyID string, snapshot any, adherence float64, confidence string) error
	LastAssessmentID(ctx context.Context, journeyID string) (string, error)
}

// PlanWriter aplica ao plano o que a adaptação propõe.
type PlanWriter interface {
	ApplyPlanChanges(ctx context.Context, userID string, frequency, minutes *int) error
}

// WithAdaptations liga as peças que só a proposta de adaptações precisa.
func (s *ProgressService) WithAdaptations(a AdaptationStore, w AssessmentWriter, p PlanWriter) *ProgressService {
	s.adaptations, s.assessments, s.plans = a, w, p
	return s
}

// Adaptations devolve as propostas pendentes, criando uma se for caso disso.
//
// **Uma de cada vez, pequena.** Propor três ao mesmo tempo é pedir que se
// ignorem as três — e cada uma guarda o `assessment` que a originou, porque um
// plano que muda sem explicação é um plano em que não se confia.
func (s *ProgressService) Adaptations(ctx context.Context, in SnapshotInput) ([]repo.AdaptationRow, error) {
	if s.adaptations == nil {
		return nil, ErrSemAdaptacoes
	}
	_, j, err := s.journeys.CurrentGoal(ctx, in.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrSemJornada
	}
	if err != nil {
		return nil, err
	}

	pendentes, err := s.adaptations.Pending(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	if len(pendentes) > 0 {
		return pendentes, nil
	}

	snap, err := s.Snapshot(ctx, in)
	if err != nil {
		return nil, err
	}

	decisao := journey.DecideAdaptation(s.cfg, journey.AdaptInput{
		JourneyID: j.ID,
		Adherence: snap.Adherence,
		Risks:     snap.Risks,
		Trend:     snap.Trend,
		NowISO:    in.Now.UTC().Format(time.RFC3339),
	})

	// `maintain` é uma decisão de não mexer, e já vem aplicada. Gravá-la como
	// proposta punha a pessoa a aprovar a ausência de mudança.
	if decisao.Kind == journey.Maintain || decisao.Applied {
		return []repo.AdaptationRow{}, nil
	}

	// Já recusou esta há pouco. O risco que a originou continua lá e o motor
	// volta a decidir o mesmo — mas repetir a pergunta no instante seguinte não
	// é propor, é insistir. Volta-se a perguntar no ciclo seguinte, que é
	// quando há factos novos para mudar a resposta.
	desde := in.Now.AddDate(0, 0, -s.cfg.OpenEndedCycleWeeks*7)
	recusada, err := s.adaptations.DismissedRecently(ctx, j.ID, string(decisao.Kind), desde)
	if err != nil {
		return nil, err
	}
	if recusada {
		return []repo.AdaptationRow{}, nil
	}

	if s.assessments != nil {
		confianca := "low"
		if snap.Trend != nil {
			confianca = string(snap.Trend.Confidence)
		}
		if err := s.assessments.InsertAssessment(ctx, j.ID, map[string]any{
			"horizon": snap.Horizon, "risks": snap.Risks, "adherence": snap.Adherence,
		}, snap.Adherence.Score, confianca); err != nil {
			return nil, err
		}
		assessmentID, err := s.assessments.LastAssessmentID(ctx, j.ID)
		if err != nil {
			return nil, err
		}
		if _, err := s.adaptations.Propose(ctx, j.ID, assessmentID, string(decisao.Kind), map[string]any{
			"reason":  decisao.Reason,
			"changes": decisao.Changes,
		}); err != nil {
			return nil, err
		}
	}
	return s.adaptations.Pending(ctx, j.ID)
}

// DecideAdaptation aplica ou dispensa uma proposta.
//
// ⚠️ **Só a pessoa aplica.** A Airo propõe; a decisão é dela, e é por isso que
// isto existe como endpoint em vez de correr sozinho no fim de um ciclo.
func (s *ProgressService) DecideAdaptation(ctx context.Context, userID, id string, aplicar bool) (repo.AdaptationRow, error) {
	if s.adaptations == nil {
		return repo.AdaptationRow{}, ErrSemAdaptacoes
	}
	row, err := s.adaptations.Decide(ctx, userID, id, aplicar)
	if err != nil {
		return repo.AdaptationRow{}, err
	}
	if !aplicar || s.plans == nil {
		return row, nil
	}

	freq, minutos := mudancasDe(row.Payload)
	if freq == nil && minutos == nil {
		return row, nil
	}
	if err := s.plans.ApplyPlanChanges(ctx, userID, freq, minutos); err != nil {
		return repo.AdaptationRow{}, err
	}
	return row, nil
}

// mudancasDe lê do payload o que se aplica ao plano.
//
// Só frequência e duração: a intensidade e a recuperação são decisões do motor
// de treino a cada sessão, não colunas do perfil.
func mudancasDe(payload map[string]any) (*int, *int) {
	changes, ok := payload["changes"].(map[string]any)
	if !ok {
		return nil, nil
	}
	return inteiroDe(changes["frequency"]), inteiroDe(changes["sessionDurationMinutes"])
}

func inteiroDe(v any) *int {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}

// ErrSemAdaptacoes — o serviço foi montado sem onde as guardar.
var ErrSemAdaptacoes = errors.New("adaptações indisponíveis")
