// Package intelligence orquestra a camada de inteligência: verifica a política,
// põe o trabalho na fila, chama o fornecedor, valida o que volta, guarda a
// proposta e — só quando a pessoa confirma — grava o resultado pelo mesmo
// caminho que um registo escrito à mão.
//
// ⚠️ **A ausência de IA não bloqueia nada.** Desligada, sem chave, sem
// orçamento ou com o fornecedor em baixo, cada operação devolve um erro que a
// app sabe ler, e o registo manual continua onde estava.
package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"time"

	core "github.com/airosp/airo-api/internal/intelligence"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"github.com/airosp/airo-api/internal/intelligence/policy"
	"github.com/airosp/airo-api/internal/intelligence/validation"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// Erros que o transporte traduz.
var (
	ErrNotFound      = errors.New("não encontrado")
	ErrInvalid       = errors.New("pedido inválido")
	ErrStaleVersion  = errors.New("a proposta mudou desde que foi lida")
	ErrClosed        = errors.New("a proposta já foi fechada")
	ErrLogIDConflict = errors.New("o identificador do registo já está em uso")
	ErrPhotoNotOwned = errors.New("a fotografia não é de quem pede")
)

// InvalidError é um pedido inválido com o campo que falhou.
type InvalidError struct {
	Field, Message string
}

func (e *InvalidError) Error() string { return e.Message }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

func invalid(field, msg string) error { return &InvalidError{Field: field, Message: msg} }

// Store é o que o serviço precisa da base de dados.
type Store interface {
	Tx(ctx context.Context, fn func(ctx context.Context) error) error

	ActiveConsents(ctx context.Context, userID string) (map[string]repo.AIConsent, error)
	ActiveConsent(ctx context.Context, userID, purpose string) (*repo.AIConsent, error)
	GrantConsent(ctx context.Context, userID, purpose, version string, now time.Time) (repo.AIConsent, error)
	RevokeConsent(ctx context.Context, userID, purpose, task string, now time.Time) (bool, error)

	CreateJob(ctx context.Context, in repo.NewAIJob) (repo.AIJob, bool, error)
	Job(ctx context.Context, userID, id string) (*repo.AIJob, error)
	CountJobsSince(ctx context.Context, userID, task string, since time.Time) (int, error)
	ClaimJob(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int) (*repo.AIJob, error)
	MarkValidating(ctx context.Context, id, model string, now time.Time) error
	FinishJob(ctx context.Context, id, status string, errorCode *string, resultRef json.RawMessage, now time.Time) error
	RetryLater(ctx context.Context, id, errorCode string, at, now time.Time) error
	SetJobStatus(ctx context.Context, id, status string, now time.Time) error
	ApplyRetention(ctx context.Context, now time.Time, keepFor, auditFor time.Duration) (repo.RetentionResult, error)

	CreateProposal(ctx context.Context, p repo.AIProposal) (repo.AIProposal, error)
	Proposal(ctx context.Context, userID, id string) (*repo.AIProposal, error)
	ProposalForJob(ctx context.Context, userID, jobID string) (*repo.AIProposal, error)
	LockProposal(ctx context.Context, userID, id string) (*repo.AIProposal, error)
	MarkApplied(ctx context.Context, id, ref string, now time.Time) error
	MarkDismissed(ctx context.Context, id string, now time.Time) error

	RecordUsage(ctx context.Context, u repo.AIUsage) error
	SpentSince(ctx context.Context, since time.Time) (int64, error)
	Audit(ctx context.Context, a repo.AIAudit) error
}

// MealLogs é o diário — o mesmo onde um registo escrito à mão vai parar.
type MealLogs interface {
	SaveLog(ctx context.Context, userID string, l repo.MealLog) error
	LogExists(ctx context.Context, userID, clientID string) (bool, error)
}

// Photos valida e descarrega as fotografias de refeições.
type Photos interface {
	MealAnalysisURL(delivered, userID string) (string, error)
	Fetch(ctx context.Context, url string) ([]byte, string, error)
}

// Config são as regras de funcionamento.
type Config struct {
	Features policy.Features
	Budget   policy.Budget
	Prices   policy.Prices
	// JobTTL é quanto tempo uma execução pode esperar na fila antes de
	// expirar. Quem tirou a fotografia está à espera; dez minutos depois já
	// registou à mão ou desistiu.
	JobTTL time.Duration
	// ProposalTTL é quanto tempo um rascunho espera por confirmação.
	ProposalTTL time.Duration
	// MaxAttempts é o tecto de tentativas de uma execução.
	MaxAttempts int
}

func DefaultConfig() Config {
	return Config{
		JobTTL:      10 * time.Minute,
		ProposalTTL: 48 * time.Hour,
		MaxAttempts: 3,
		Budget:      policy.Budget{DailyPerUser: 20, AlertRatio: 0.8},
	}
}

type Service struct {
	store    Store
	logs     MealLogs
	photos   Photos
	provider core.Provider
	catalog  *validation.Catalog
	clock    clock.Clock
	log      *slog.Logger
	cfg      Config
	// wake acorda o executor quando chega trabalho, para não esperar pela
	// próxima volta do relógio.
	wake chan struct{}
}

// New monta o serviço. `provider` nil é a IA desligada: as rotas respondem, e
// dizem que a funcionalidade não está disponível.
func New(store Store, logs MealLogs, photos Photos, provider core.Provider, c clock.Clock, log *slog.Logger, cfg Config) (*Service, error) {
	cat, err := validation.LoadCatalog()
	if err != nil {
		return nil, err
	}
	if provider == nil {
		cfg.Features.Enabled = false
	}
	if photos == nil {
		cfg.Features.MealPhoto = false
	}
	return &Service{
		store: store, logs: logs, photos: photos, provider: provider, catalog: cat,
		clock: c, log: log, cfg: cfg, wake: make(chan struct{}, 1),
	}, nil
}

func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// ── capacidades e consentimento ─────────────────────────────────────────────

type FeatureState struct {
	Available     bool   `json:"available"`
	Consented     bool   `json:"consented"`
	PolicyVersion string `json:"policyVersion"`
}

type Capabilities struct {
	Enabled   bool         `json:"enabled"`
	MealPhoto FeatureState `json:"mealPhoto"`
	MealText  FeatureState `json:"mealText"`
	// O que ainda não existe diz-se: a app não tem de adivinhar.
	ProgressInsights   bool `json:"progressInsights"`
	Explanations       bool `json:"explanations"`
	SpecialistInsights bool `json:"specialistInsights"`
}

// Capabilities diz à app o que pode mostrar a esta pessoa.
func (s *Service) Capabilities(ctx context.Context, userID string) (Capabilities, error) {
	consents, err := s.store.ActiveConsents(ctx, userID)
	if err != nil {
		return Capabilities{}, err
	}
	state := func(task, purpose string) FeatureState {
		c, ok := consents[purpose]
		return FeatureState{
			Available:     s.cfg.Features.Allows(task),
			Consented:     ok && policy.ConsentValid(purpose, c.PolicyVersion),
			PolicyVersion: policy.PolicyVersions[purpose],
		}
	}
	return Capabilities{
		Enabled:   s.cfg.Features.Enabled,
		MealPhoto: state(contracts.TaskMealPhoto, policy.PurposeMealPhoto),
		MealText:  state(contracts.TaskMealText, policy.PurposeMealText),
	}, nil
}

type ConsentState struct {
	Purpose       string     `json:"purpose"`
	Granted       bool       `json:"granted"`
	PolicyVersion string     `json:"policyVersion"`
	GrantedAt     *time.Time `json:"grantedAt,omitempty"`
	// Current diz se foi dado ao texto que está publicado agora.
	Current bool `json:"current"`
}

// Consents lista as autorizações, uma por finalidade conhecida.
func (s *Service) Consents(ctx context.Context, userID string) ([]ConsentState, error) {
	active, err := s.store.ActiveConsents(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []ConsentState{}
	for _, p := range []string{policy.PurposeMealPhoto, policy.PurposeMealText} {
		st := ConsentState{Purpose: p, PolicyVersion: policy.PolicyVersions[p]}
		if c, ok := active[p]; ok {
			at := c.GrantedAt.UTC()
			st.Granted, st.GrantedAt = true, &at
			st.Current = policy.ConsentValid(p, c.PolicyVersion)
			st.PolicyVersion = c.PolicyVersion
		}
		out = append(out, st)
	}
	return out, nil
}

// SetConsent dá ou retira uma autorização.
//
// Dar exige a versão do texto que a pessoa leu, e tem de ser a publicada: uma
// autorização a um texto antigo é uma autorização a outra coisa.
func (s *Service) SetConsent(ctx context.Context, userID, purpose string, granted bool, version string) (ConsentState, error) {
	if !policy.KnownPurpose(purpose) {
		return ConsentState{}, invalid("purpose", "Finalidade desconhecida.")
	}
	now := s.clock.Now()
	if !granted {
		task := taskFor(purpose)
		revoked, err := s.store.RevokeConsent(ctx, userID, purpose, task, now)
		if err != nil {
			return ConsentState{}, err
		}
		if revoked {
			s.audit(ctx, repo.AIAudit{UserID: userID, Kind: "consent_revoked", Detail: map[string]any{"purpose": purpose}})
		}
		return ConsentState{Purpose: purpose, PolicyVersion: policy.PolicyVersions[purpose]}, nil
	}
	if version != policy.PolicyVersions[purpose] {
		return ConsentState{}, invalid("policyVersion", "O texto da autorização mudou. Lê a versão actual antes de autorizar.")
	}
	c, err := s.store.GrantConsent(ctx, userID, purpose, version, now)
	if err != nil {
		return ConsentState{}, err
	}
	s.audit(ctx, repo.AIAudit{UserID: userID, Kind: "consent_granted",
		Detail: map[string]any{"purpose": purpose, "policyVersion": version}})
	at := c.GrantedAt.UTC()
	return ConsentState{Purpose: purpose, Granted: true, PolicyVersion: version, GrantedAt: &at, Current: true}, nil
}

func taskFor(purpose string) string {
	switch purpose {
	case policy.PurposeMealPhoto:
		return contracts.TaskMealPhoto
	case policy.PurposeMealText:
		return contracts.TaskMealText
	}
	return ""
}

// ── submeter ────────────────────────────────────────────────────────────────

var (
	slots        = map[string]bool{"breakfast": true, "lunch": true, "snack": true, "dinner": true, "supper": true}
	idempotentRe = regexp.MustCompile(`^[A-Za-z0-9_\-:.]{8,128}$`)
)

// Submitted é a resposta a um pedido de análise.
type Submitted struct {
	JobID       string `json:"jobId"`
	Status      string `json:"status"`
	PollAfterMs int    `json:"pollAfterMs"`
}

// SubmitPhoto põe uma fotografia na fila para reconhecimento.
func (s *Service) SubmitPhoto(ctx context.Context, userID, key, photoURL, thumbURL, slot, localDay string) (Submitted, error) {
	if s.photos == nil {
		return Submitted{}, core.ErrDisabled
	}
	analysisURL, err := s.photos.MealAnalysisURL(photoURL, userID)
	if err != nil {
		return Submitted{}, ErrPhotoNotOwned
	}
	// A pequena é só para mostrar no rascunho; se não for desta pessoa, cai.
	if thumbURL != "" {
		if _, err := s.photos.MealAnalysisURL(thumbURL, userID); err != nil {
			thumbURL = ""
		}
	}
	input, _ := json.Marshal(map[string]string{
		"photoUrl": photoURL, "photoThumbUrl": thumbURL, "analysisUrl": analysisURL,
		"slot": slot, "localDay": localDay,
	})
	return s.submit(ctx, userID, key, contracts.TaskMealPhoto, slot, localDay, input)
}

// SubmitText põe um registo escrito na fila para interpretação.
func (s *Service) SubmitText(ctx context.Context, userID, key, text, slot, localDay string) (Submitted, error) {
	clean := policy.RedactText(text)
	if len([]rune(clean)) < 2 {
		return Submitted{}, invalid("text", "Escreve o que comeste.")
	}
	input, _ := json.Marshal(map[string]string{"text": clean, "slot": slot, "localDay": localDay})
	return s.submit(ctx, userID, key, contracts.TaskMealText, slot, localDay, input)
}

func (s *Service) submit(ctx context.Context, userID, key, task, slot, localDay string, input json.RawMessage) (Submitted, error) {
	if !idempotentRe.MatchString(key) {
		return Submitted{}, invalid("Idempotency-Key", "Falta a chave de idempotência, ou tem uma forma inválida.")
	}
	if !slots[slot] {
		return Submitted{}, invalid("slot", "Refeição desconhecida.")
	}
	if _, err := time.Parse("2006-01-02", localDay); err != nil {
		return Submitted{}, invalid("localDay", "Dia inválido.")
	}
	if !s.cfg.Features.Allows(task) {
		return Submitted{}, core.ErrDisabled
	}

	purpose := policy.PurposeFor(task)
	consent, err := s.store.ActiveConsent(ctx, userID, purpose)
	if err != nil {
		return Submitted{}, err
	}
	if consent == nil || !policy.ConsentValid(purpose, consent.PolicyVersion) {
		return Submitted{}, core.ErrNoConsent
	}

	now := s.clock.Now()
	// O mesmo pedido outra vez devolve a execução que já existe — antes dos
	// limites, para um reenvio não contar como pedido novo.
	if j, created, err := s.store.CreateJob(ctx, repo.NewAIJob{
		UserID: userID, TaskType: task, SchemaVersion: contracts.SchemaVersion,
		ConsentID: consent.ID, IdempotencyKey: key, InputRef: input,
		ExpiresAt: now.Add(s.cfg.JobTTL),
	}); err != nil {
		return Submitted{}, err
	} else if !created {
		return submittedFrom(j), nil
	} else if err := s.admit(ctx, userID, task, now); err != nil {
		// Passou do limite: a execução nasce cancelada, para o reenvio da
		// mesma chave devolver o mesmo "não" em vez de abrir outra.
		code := errorCode(err)
		_ = s.store.FinishJob(ctx, j.ID, contracts.JobCancelled, &code, nil, now)
		return Submitted{}, err
	} else {
		s.audit(ctx, repo.AIAudit{UserID: userID, JobID: j.ID, Kind: "job_created",
			Detail: map[string]any{"task": task, "consentId": consent.ID}})
		metrics.job(task, contracts.JobQueued)
		s.notify()
		return submittedFrom(j), nil
	}
}

// admit verifica os limites de quem pede e da instalação.
func (s *Service) admit(ctx context.Context, userID, task string, now time.Time) error {
	if limit := s.cfg.Budget.DailyPerUser; limit > 0 {
		n, err := s.store.CountJobsSince(ctx, userID, task, policy.DayStart(now))
		if err != nil {
			return err
		}
		// O pedido acabado de criar já conta.
		if n > limit {
			return core.ErrDailyLimit
		}
	}
	spent, err := s.store.SpentSince(ctx, policy.MonthStart(now))
	if err != nil {
		return err
	}
	if s.cfg.Budget.Exhausted(spent) {
		return core.ErrBudgetExhausted
	}
	return nil
}

func submittedFrom(j repo.AIJob) Submitted {
	out := Submitted{JobID: j.ID, Status: j.Status}
	if !contracts.Terminal(j.Status) {
		out.PollAfterMs = 1500
	}
	return out
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, core.ErrDailyLimit):
		return "daily_limit"
	case errors.Is(err, core.ErrBudgetExhausted):
		return "budget_exhausted"
	case errors.Is(err, core.ErrNoConsent):
		return "consent_required"
	case errors.Is(err, core.ErrDisabled):
		return "disabled"
	}
	return "internal"
}

// ── consultar ───────────────────────────────────────────────────────────────

type JobView struct {
	JobID       string  `json:"jobId"`
	Status      string  `json:"status"`
	ProposalID  *string `json:"proposalId,omitempty"`
	ErrorCode   *string `json:"errorCode,omitempty"`
	PollAfterMs int     `json:"pollAfterMs,omitempty"`
}

func (s *Service) Job(ctx context.Context, userID, id string) (JobView, error) {
	if !uuidRe.MatchString(id) {
		return JobView{}, ErrNotFound
	}
	j, err := s.store.Job(ctx, userID, id)
	if err != nil {
		return JobView{}, err
	}
	if j == nil {
		return JobView{}, ErrNotFound
	}
	out := JobView{JobID: j.ID, Status: j.Status, ErrorCode: j.ErrorCode}
	if !contracts.Terminal(j.Status) {
		out.PollAfterMs = 1500
	}
	if p, err := s.store.ProposalForJob(ctx, userID, j.ID); err != nil {
		return JobView{}, err
	} else if p != nil {
		out.ProposalID = &p.ID
	}
	return out, nil
}

type ProposalView struct {
	ProposalID string              `json:"proposalId"`
	JobID      string              `json:"jobId"`
	Status     string              `json:"status"`
	Version    int                 `json:"version"`
	Draft      contracts.MealDraft `json:"draft"`
	AppliedRef *string             `json:"appliedLogId,omitempty"`
	ExpiresAt  string              `json:"expiresAt"`
}

func (s *Service) Proposal(ctx context.Context, userID, id string) (ProposalView, error) {
	if !uuidRe.MatchString(id) {
		return ProposalView{}, ErrNotFound
	}
	p, err := s.store.Proposal(ctx, userID, id)
	if err != nil {
		return ProposalView{}, err
	}
	if p == nil {
		return ProposalView{}, ErrNotFound
	}
	return proposalView(*p)
}

func proposalView(p repo.AIProposal) (ProposalView, error) {
	var d contracts.MealDraft
	if err := json.Unmarshal(p.Payload, &d); err != nil {
		return ProposalView{}, err
	}
	return ProposalView{
		ProposalID: p.ID, JobID: p.JobID, Status: p.Status, Version: p.Version,
		Draft: d, AppliedRef: p.AppliedRef, ExpiresAt: p.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Service) audit(ctx context.Context, a repo.AIAudit) {
	// A auditoria não pode derrubar a operação que regista — mas uma falha
	// dela tem de se ver.
	if err := s.store.Audit(ctx, a); err != nil {
		s.log.Error("inteligência: auditoria falhou", "kind", a.Kind, "error", err)
	}
}
