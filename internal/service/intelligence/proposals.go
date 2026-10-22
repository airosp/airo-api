package intelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/intelligence/contracts"
	"github.com/airosp/airo-api/internal/intelligence/validation"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// ApplyItem é um alimento como a pessoa o confirmou.
type ApplyItem struct {
	FoodID string  `json:"foodId"`
	Grams  float64 `json:"grams"`
}

// ApplyInput é a confirmação de um rascunho.
type ApplyInput struct {
	// Version é a versão do rascunho que a pessoa viu.
	Version int `json:"version"`
	// LogID é o identificador do registo no diário. É do telemóvel, como o de
	// um registo escrito à mão: é o que deixa a mesma confirmação, reenviada,
	// cair no mesmo registo.
	LogID      string      `json:"logId"`
	Slot       string      `json:"slot"`
	LocalDay   string      `json:"localDay"`
	RecordedAt string      `json:"recordedAt"`
	Label      string      `json:"label,omitempty"`
	Items      []ApplyItem `json:"items"`
}

// AppliedLog é o que ficou no diário.
type AppliedLog struct {
	LogID         string      `json:"logId"`
	Slot          string      `json:"slot"`
	Status        string      `json:"status"`
	Source        string      `json:"source"`
	Label         string      `json:"label"`
	PortionLabel  string      `json:"portionLabel"`
	Kcal          int         `json:"kcal"`
	Macros        macros      `json:"macros"`
	PhotoURL      string      `json:"photoUrl,omitempty"`
	PhotoThumbURL string      `json:"photoThumbUrl,omitempty"`
	RecordedAt    string      `json:"recordedAt"`
	LocalDay      string      `json:"localDay"`
	Items         []loggedRow `json:"items"`
	// AlreadyApplied é a mesma confirmação a chegar outra vez.
	AlreadyApplied bool `json:"alreadyApplied"`
}

type macros struct {
	Protein float64 `json:"protein"`
	Carbs   float64 `json:"carbs"`
	Fat     float64 `json:"fat"`
}

type loggedRow struct {
	FoodID string  `json:"foodId"`
	Name   string  `json:"name"`
	Grams  float64 `json:"grams"`
	Kcal   float64 `json:"kcal"`
}

var logIDRe = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,128}$`)

/*
 * Apply confirma um rascunho e grava-o no diário.
 *
 * ⚠️ **É aqui que os números nascem, e não antes.** O que a pessoa confirmou —
 * alimentos do catálogo e gramas — passa por `nutrition.ValueLoggedMeal`, o
 * mesmo cálculo do plano, e só esse resultado entra no registo. Nada do que o
 * modelo disse chega lá a não ser através do que a pessoa aceitou.
 *
 * Numa transacção, com a proposta trancada: dois toques em "confirmar" não
 * gravam duas refeições, e uma confirmação a meio não deixa a proposta
 * aplicada sem registo nem o registo sem proposta.
 */
func (s *Service) Apply(ctx context.Context, userID, proposalID string, in ApplyInput) (AppliedLog, error) {
	if !uuidRe.MatchString(proposalID) {
		return AppliedLog{}, ErrNotFound
	}
	if !logIDRe.MatchString(in.LogID) {
		return AppliedLog{}, invalid("logId", "Identificador do registo inválido.")
	}
	if !slots[in.Slot] {
		return AppliedLog{}, invalid("slot", "Refeição desconhecida.")
	}
	day, err := time.Parse("2006-01-02", in.LocalDay)
	if err != nil {
		return AppliedLog{}, invalid("localDay", "Dia inválido.")
	}
	recordedAt, err := time.Parse(time.RFC3339, in.RecordedAt)
	if err != nil {
		return AppliedLog{}, invalid("recordedAt", "Momento do registo inválido.")
	}
	if len(in.Items) == 0 {
		return AppliedLog{}, invalid("items", "Confirma pelo menos um alimento.")
	}
	if len(in.Items) > contracts.MaxItems {
		return AppliedLog{}, invalid("items", fmt.Sprintf("No máximo %d alimentos por refeição.", contracts.MaxItems))
	}
	logged := make([]nutrition.LoggedFood, 0, len(in.Items))
	for i, it := range in.Items {
		if _, ok := s.catalog.Food(it.FoodID); !ok {
			return AppliedLog{}, invalid(fmt.Sprintf("items[%d].foodId", i), "Alimento que não está no catálogo.")
		}
		if it.Grams <= 0 || it.Grams > nutrition.MaxLoggedGrams {
			return AppliedLog{}, invalid(fmt.Sprintf("items[%d].grams", i), "Quantidade fora do esperado.")
		}
		logged = append(logged, nutrition.LoggedFood{FoodID: it.FoodID, Grams: it.Grams})
	}
	value, err := nutrition.ValueLoggedMeal(logged)
	if err != nil {
		return AppliedLog{}, invalid("items", "Não foi possível calcular esta refeição.")
	}

	var out AppliedLog
	err = s.store.Tx(ctx, func(ctx context.Context) error {
		p, err := s.store.LockProposal(ctx, userID, proposalID)
		if err != nil {
			return err
		}
		if p == nil {
			return ErrNotFound
		}
		var draft contracts.MealDraft
		if err := json.Unmarshal(p.Payload, &draft); err != nil {
			return err
		}
		out = s.appliedFrom(draft, in, value, recordedAt)

		switch p.Status {
		case contracts.JobApplied:
			// A mesma confirmação outra vez: o registo já lá está.
			if p.AppliedRef != nil && *p.AppliedRef == in.LogID {
				out.AlreadyApplied = true
				return nil
			}
			return ErrClosed
		case contracts.JobReadyForReview, contracts.JobNeedsCorrection:
		default:
			return ErrClosed
		}
		if s.clock.Now().After(p.ExpiresAt) {
			return ErrClosed
		}
		if in.Version != p.Version {
			return ErrStaleVersion
		}
		if exists, err := s.logs.LogExists(ctx, userID, in.LogID); err != nil {
			return err
		} else if exists {
			// Um identificador que já existe é outro registo. Gravar por cima
			// apagava-o.
			return ErrLogIDConflict
		}

		source := "manual"
		if draft.Source == validation.SourcePhoto {
			source = "photo"
		}
		label, portion := out.Label, out.PortionLabel
		l := repo.MealLog{
			ClientID: in.LogID, Slot: in.Slot, Status: "custom", Portion: 1,
			Kcal: value.Kcal, Protein: value.Macros.Protein, Carbs: value.Macros.Carbs, Fat: value.Macros.Fat,
			Source: source, Label: &label, PortionLabel: &portion,
			RecordedAt: recordedAt, LocalDay: day,
		}
		if draft.PhotoURL != "" {
			l.PhotoURL = &draft.PhotoURL
		}
		if draft.PhotoThumbURL != "" {
			l.PhotoThumb = &draft.PhotoThumbURL
		}
		if err := s.logs.SaveLog(ctx, userID, l); err != nil {
			return err
		}
		now := s.clock.Now()
		if err := s.store.MarkApplied(ctx, p.ID, in.LogID, now); err != nil {
			return err
		}
		if err := s.store.SetJobStatus(ctx, p.JobID, contracts.JobApplied, now); err != nil {
			return err
		}
		edited := editedByPerson(draft, in.Items)
		metrics.applied(p.TaskType, edited)
		return s.store.Audit(ctx, repo.AIAudit{
			UserID: userID, JobID: p.JobID, ProposalID: p.ID, Kind: "proposal_applied",
			Detail: map[string]any{
				"logId": in.LogID, "items": len(in.Items), "draftItems": len(draft.Items),
				"editedByPerson": edited, "kcal": value.Kcal,
			},
		})
	})
	if err != nil {
		return AppliedLog{}, err
	}
	return out, nil
}

func (s *Service) appliedFrom(draft contracts.MealDraft, in ApplyInput, value nutrition.LoggedMeal, recordedAt time.Time) AppliedLog {
	names := make([]string, 0, len(value.Items))
	rows := make([]loggedRow, 0, len(value.Items))
	var grams float64
	for _, it := range value.Items {
		names = append(names, it.Name)
		rows = append(rows, loggedRow{FoodID: it.FoodID, Name: it.Name, Grams: it.Grams, Kcal: it.Kcal})
		grams += it.Grams
	}
	label := validation.SanitizeLabel(in.Label)
	if label == "" {
		label = validation.SanitizeLabel(strings.Join(names, ", "))
	}
	source := "manual"
	if draft.Source == validation.SourcePhoto {
		source = "photo"
	}
	return AppliedLog{
		LogID: in.LogID, Slot: in.Slot, Status: "custom", Source: source,
		Label: label, PortionLabel: fmt.Sprintf("%.0f g", grams),
		Kcal: value.Kcal, Macros: macros{value.Macros.Protein, value.Macros.Carbs, value.Macros.Fat},
		PhotoURL: draft.PhotoURL, PhotoThumbURL: draft.PhotoThumbURL,
		RecordedAt: recordedAt.UTC().Format(time.RFC3339), LocalDay: in.LocalDay, Items: rows,
	}
}

// editedByPerson diz se a pessoa mudou o rascunho antes de o confirmar. É a
// medida das "correcções humanas": quanto o reconhecimento falha na prática.
func editedByPerson(draft contracts.MealDraft, items []ApplyItem) bool {
	if len(draft.Items) != len(items) {
		return true
	}
	for i, d := range draft.Items {
		if d.FoodID != items[i].FoodID || d.Grams == nil || *d.Grams != items[i].Grams {
			return true
		}
	}
	return false
}

// Dismiss fecha um rascunho sem gravar nada. Dispensar duas vezes é estar
// dispensado.
func (s *Service) Dismiss(ctx context.Context, userID, proposalID string) error {
	if !uuidRe.MatchString(proposalID) {
		return ErrNotFound
	}
	return s.store.Tx(ctx, func(ctx context.Context) error {
		p, err := s.store.LockProposal(ctx, userID, proposalID)
		if err != nil {
			return err
		}
		if p == nil {
			return ErrNotFound
		}
		switch p.Status {
		case contracts.JobDismissed:
			return nil
		case contracts.JobApplied:
			return ErrClosed
		}
		now := s.clock.Now()
		if err := s.store.MarkDismissed(ctx, p.ID, now); err != nil {
			return err
		}
		if err := s.store.SetJobStatus(ctx, p.JobID, contracts.JobDismissed, now); err != nil {
			return err
		}
		metrics.dismissed(p.TaskType)
		return s.store.Audit(ctx, repo.AIAudit{UserID: userID, JobID: p.JobID, ProposalID: p.ID, Kind: "proposal_dismissed"})
	})
}
