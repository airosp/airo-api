package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/airosp/airo-api/internal/intelligence"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"github.com/airosp/airo-api/internal/intelligence/policy"
	"github.com/airosp/airo-api/internal/intelligence/validation"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// O que cada execução guarda à entrada.
type jobInput struct {
	PhotoURL      string `json:"photoUrl"`
	PhotoThumbURL string `json:"photoThumbUrl"`
	AnalysisURL   string `json:"analysisUrl"`
	Text          string `json:"text"`
	Slot          string `json:"slot"`
	LocalDay      string `json:"localDay"`
}

// maxOutputTokens é o tecto da resposta. Uma refeição com doze itens cabe
// folgada; o resto é margem para os modelos que raciocinam antes de responder.
const maxOutputTokens = 4000

// jobTimeout é o prazo de uma execução inteira, tentativas incluídas.
const jobTimeout = 2 * time.Minute

/*
 * process corre uma execução reservada, do princípio ao fim.
 *
 * A ordem é a política: primeiro voltar a ver se ainda pode correr — a
 * autorização pode ter sido retirada enquanto a execução esperava —, depois o
 * orçamento, e só então o fornecedor. O que volta é validado e resolvido
 * contra o catálogo antes de ser guardado como proposta.
 */
func (s *Service) process(ctx context.Context, j repo.AIJob) {
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	var in jobInput
	if err := json.Unmarshal(j.InputRef, &in); err != nil {
		s.fail(ctx, j, "invalid_input", err)
		return
	}

	if !s.cfg.Features.Allows(j.TaskType) {
		s.finish(ctx, j, contracts.JobCancelled, "disabled", nil)
		return
	}
	purpose := policy.PurposeFor(j.TaskType)
	consent, err := s.store.ActiveConsent(ctx, j.UserID, purpose)
	if err != nil {
		s.retry(ctx, j, "store_unavailable", err)
		return
	}
	if consent == nil || !policy.ConsentValid(purpose, consent.PolicyVersion) {
		s.finish(ctx, j, contracts.JobCancelled, "consent_revoked", nil)
		return
	}
	spent, err := s.store.SpentSince(ctx, policy.MonthStart(s.clock.Now()))
	if err != nil {
		s.retry(ctx, j, "store_unavailable", err)
		return
	}
	if s.cfg.Budget.Exhausted(spent) {
		s.finish(ctx, j, contracts.JobFailed, "budget_exhausted", nil)
		s.log.Warn("inteligência: orçamento do mês esgotado — as análises param até ao próximo mês",
			"gasto_usd", float64(spent)/1e6)
		return
	} else if s.cfg.Budget.NearLimit(spent) {
		s.log.Warn("inteligência: orçamento do mês perto do fim",
			"gasto_usd", float64(spent)/1e6, "tecto_usd", float64(s.cfg.Budget.MonthlyMicroUSD)/1e6)
	}

	var (
		res    core.StructuredResult
		callFn func() (core.StructuredResult, error)
		source string
	)
	schema := contracts.MealExtractionSchema(s.catalog.FoodIDs(), s.catalog.RecipeIDs())
	switch j.TaskType {
	case contracts.TaskMealPhoto:
		source = validation.SourcePhoto
		image, mime, err := s.photos.Fetch(ctx, in.AnalysisURL)
		if err != nil {
			s.retry(ctx, j, "photo_unavailable", err)
			return
		}
		callFn = func() (core.StructuredResult, error) {
			return s.provider.AnalyzeImage(ctx, photoRequest(s.catalog, schema, in, image, mime))
		}
	case contracts.TaskMealText:
		source = validation.SourceText
		callFn = func() (core.StructuredResult, error) {
			return s.provider.GenerateStructured(ctx, textRequest(s.catalog, schema, in))
		}
	default:
		s.fail(ctx, j, "unknown_task", fmt.Errorf("tarefa %q", j.TaskType))
		return
	}

	started := s.clock.Now()
	res, err = callFn()
	s.recordUsage(ctx, j, res, err, started)
	if err != nil {
		s.providerFailed(ctx, j, err)
		return
	}

	now := s.clock.Now()
	if err := s.store.MarkValidating(ctx, j.ID, res.Model, now); err != nil {
		s.retry(ctx, j, "store_unavailable", err)
		return
	}
	extraction, err := validation.ParseMealExtraction(res.Data)
	if err != nil {
		s.fail(ctx, j, "invalid_output", err)
		return
	}
	resolved := s.catalog.Resolve(validation.ResolveInput{Source: source, Extraction: extraction, UserText: in.Text})

	draft := contracts.MealDraft{
		SchemaVersion: contracts.SchemaVersion, Source: source, Slot: in.Slot, LocalDay: in.LocalDay,
		PhotoURL: in.PhotoURL, PhotoThumbURL: in.PhotoThumbURL,
		Items: resolved.Items, UnknownItems: resolved.Unknown, Warnings: resolved.Warnings,
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		s.fail(ctx, j, "internal", err)
		return
	}

	err = s.store.Tx(ctx, func(ctx context.Context) error {
		p, err := s.store.CreateProposal(ctx, repo.AIProposal{
			JobID: j.ID, UserID: j.UserID, TaskType: j.TaskType, Status: resolved.Status,
			SchemaVersion: contracts.SchemaVersion, Payload: payload,
			ExpiresAt: now.Add(s.cfg.ProposalTTL),
		})
		if err != nil {
			return err
		}
		ref, _ := json.Marshal(map[string]string{"proposalId": p.ID})
		if err := s.store.FinishJob(ctx, j.ID, resolved.Status, nil, ref, now); err != nil {
			return err
		}
		return s.store.Audit(ctx, repo.AIAudit{
			UserID: j.UserID, JobID: j.ID, ProposalID: p.ID, Kind: "proposal_created",
			Detail: map[string]any{
				"task": j.TaskType, "status": resolved.Status, "model": res.Model,
				"schemaVersion": contracts.SchemaVersion, "items": len(resolved.Items),
				"unknown": len(resolved.Unknown), "warnings": resolved.Warnings,
				"requestId": res.RequestID, "attempts": j.Attempts,
			},
		})
	})
	if err != nil {
		s.retry(ctx, j, "store_unavailable", err)
		return
	}
	metrics.job(j.TaskType, resolved.Status)
	metrics.items(len(resolved.Items), len(resolved.Unknown))
}

func (s *Service) recordUsage(ctx context.Context, j repo.AIJob, res core.StructuredResult, callErr error, started time.Time) {
	outcome := "ok"
	if callErr != nil {
		outcome = string(core.KindOf(callErr))
	}
	cost := s.cfg.Prices.CostMicroUSD(res.InputTokens, res.OutputTokens)
	latency := res.Latency
	if latency == 0 {
		latency = s.clock.Now().Sub(started)
	}
	model := res.Model
	if model == "" {
		model = "desconhecido"
	}
	metrics.call(j.TaskType, outcome, latency, res.InputTokens, res.OutputTokens, cost)
	// Fora do contexto da execução: o consumo regista-se mesmo que o prazo
	// tenha acabado entretanto — a chamada já foi paga.
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.RecordUsage(uctx, repo.AIUsage{
		UserID: j.UserID, JobID: j.ID, TaskType: j.TaskType, Model: model, RequestID: res.RequestID,
		InputTokens: res.InputTokens, OutputTokens: res.OutputTokens, CostMicroUSD: cost,
		LatencyMs: int(latency.Milliseconds()), Outcome: outcome,
	}); err != nil {
		s.log.Error("inteligência: consumo por registar", "job", j.ID, "error", err)
	}
}

// providerFailed decide o destino de uma chamada falhada.
func (s *Service) providerFailed(ctx context.Context, j repo.AIJob, err error) {
	switch core.KindOf(err) {
	case core.Transient:
		s.retry(ctx, j, "provider_unavailable", err)
	case core.Refused:
		s.fail(ctx, j, "refused", err)
	case core.Incomplete:
		s.fail(ctx, j, "incomplete", err)
	case core.Quota:
		s.fail(ctx, j, "provider_quota", err)
		s.log.Error("inteligência: a conta do fornecedor está sem saldo", "error", err)
	default:
		s.fail(ctx, j, "provider_error", err)
	}
}

// retry devolve a execução à fila, ou desiste se já não há tentativas.
func (s *Service) retry(ctx context.Context, j repo.AIJob, code string, err error) {
	if j.Attempts >= s.cfg.MaxAttempts {
		s.fail(ctx, j, code, err)
		return
	}
	// 5 s, 20 s: quem está à espera de uma fotografia não espera minutos.
	wait := time.Duration(5*j.Attempts*j.Attempts) * time.Second
	now := s.clock.Now()
	s.log.Warn("inteligência: tentativa falhou, volta à fila",
		"job", j.ID, "task", j.TaskType, "attempt", j.Attempts, "codigo", code, "error", err)
	if e := s.store.RetryLater(context.WithoutCancel(ctx), j.ID, code, now.Add(wait), now); e != nil {
		s.log.Error("inteligência: não foi possível devolver à fila", "job", j.ID, "error", e)
	}
}

func (s *Service) fail(ctx context.Context, j repo.AIJob, code string, err error) {
	s.log.Error("inteligência: execução falhou", "job", j.ID, "task", j.TaskType, "codigo", code, "error", err)
	s.finish(ctx, j, contracts.JobFailed, code, err)
}

func (s *Service) finish(ctx context.Context, j repo.AIJob, status, code string, cause error) {
	ctx = context.WithoutCancel(ctx)
	if e := s.store.FinishJob(ctx, j.ID, status, &code, nil, s.clock.Now()); e != nil {
		s.log.Error("inteligência: não foi possível fechar a execução", "job", j.ID, "error", e)
	}
	detail := map[string]any{"task": j.TaskType, "status": status, "code": code, "attempts": j.Attempts}
	var pe *core.ProviderError
	if errors.As(cause, &pe) {
		detail["providerStatus"], detail["providerCode"] = pe.Status, pe.Code
	}
	s.audit(ctx, repo.AIAudit{UserID: j.UserID, JobID: j.ID, Kind: "job_" + status, Detail: detail})
	metrics.job(j.TaskType, status)
}
