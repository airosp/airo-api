package handlers

import (
	"errors"
	"net/http"

	core "github.com/airosp/airo-api/internal/intelligence"
	ai "github.com/airosp/airo-api/internal/service/intelligence"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

/*
 * Intelligence expõe a camada de inteligência.
 *
 * As rotas existem sempre, com ou sem IA ligada. Desligada, as capacidades
 * dizem-no e os pedidos de análise respondem 503 com um código que a app lê —
 * é o que a faz mostrar o registo manual em vez de um erro. Uma rota que só
 * existisse com a IA ligada mudava a forma da API com a configuração, e a app
 * não teria como saber em qual das duas está.
 */
type Intelligence struct {
	Service *ai.Service
}

// Capabilities diz o que a app pode mostrar a esta pessoa.
func (h Intelligence) Capabilities(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	out, err := h.Service.Capabilities(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as capacidades.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func (h Intelligence) Consent(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	out, err := h.Service.Consents(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as autorizações.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, dto.ConsentsResponse{Consents: out})
}

// SetConsent dá ou retira uma autorização. `PUT` do estado: repetir é
// inofensivo, e por isso fica fora da cadeia de idempotência.
func (h Intelligence) SetConsent(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	var req dto.ConsentRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if req.Granted == nil {
		apierr.Write(w, apierr.ValidationFailed, "Diz se autorizas ou não.", "granted")
		return
	}
	out, err := h.Service.SetConsent(r.Context(), userID, req.Purpose, *req.Granted, req.PolicyVersion)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// Recognize pede o reconhecimento de uma fotografia já guardada.
//
// Responde 202: a análise corre fora do pedido, e a app pergunta pelo estado.
// Uma fotografia pode demorar vários segundos, e um pedido aberto esse tempo
// numa rede fraca é um pedido que cai a meio.
func (h Intelligence) Recognize(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	var req dto.RecognizeMealRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	out, err := h.Service.SubmitPhoto(r.Context(), userID, r.Header.Get("Idempotency-Key"),
		req.PhotoURL, req.PhotoThumbURL, req.Slot, req.LocalDay)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusAccepted, out)
}

// Parse pede a interpretação de um registo escrito.
func (h Intelligence) Parse(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	var req dto.ParseMealRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	out, err := h.Service.SubmitText(r.Context(), userID, r.Header.Get("Idempotency-Key"),
		req.Text, req.Slot, req.LocalDay)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusAccepted, out)
}

func (h Intelligence) Job(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	out, err := h.Service.Job(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func (h Intelligence) Proposal(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	out, err := h.Service.Proposal(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// Apply confirma o rascunho e grava-o no diário. Idempotente pelo `logId`: a
// mesma confirmação, reenviada, devolve o mesmo registo.
func (h Intelligence) Apply(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	var req ai.ApplyInput
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	out, err := h.Service.Apply(r.Context(), userID, r.PathValue("id"), req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func (h Intelligence) Dismiss(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if err := h.Service.Dismiss(r.Context(), userID, r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeErr traduz os erros do serviço. Cada um tem o seu código, porque a app
// faz coisas diferentes com cada um: pedir autorização, mostrar o registo
// manual, ou voltar a ler a proposta.
func (h Intelligence) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var inv *ai.InvalidError
	switch {
	case errors.As(err, &inv):
		apierr.Write(w, apierr.ValidationFailed, inv.Message, inv.Field)
	case errors.Is(err, ai.ErrNotFound):
		apierr.Write(w, apierr.NotFound, "Não encontrado.", "")
	case errors.Is(err, ai.ErrPhotoNotOwned):
		apierr.Write(w, apierr.ValidationFailed, "Esta fotografia não é uma das tuas refeições guardadas.", "photoUrl")
	case errors.Is(err, core.ErrDisabled):
		apierr.Write(w, apierr.AIUnavailable, "O reconhecimento não está disponível. Regista à mão.", "")
	case errors.Is(err, core.ErrNoConsent):
		apierr.Write(w, apierr.AIConsentRequired, "Falta a tua autorização para esta análise.", "")
	case errors.Is(err, core.ErrDailyLimit):
		apierr.Write(w, apierr.AIDailyLimit, "Chegaste ao limite de análises de hoje. Regista à mão.", "")
	case errors.Is(err, core.ErrBudgetExhausted):
		apierr.Write(w, apierr.AIUnavailable, "O reconhecimento está em pausa. Regista à mão.", "")
	case errors.Is(err, ai.ErrStaleVersion):
		apierr.Write(w, apierr.AIProposalStale, "A proposta mudou. Volta a abri-la.", "version")
	case errors.Is(err, ai.ErrClosed):
		apierr.Write(w, apierr.AIProposalClosed, "Esta proposta já foi fechada.", "")
	case errors.Is(err, ai.ErrLogIDConflict):
		apierr.Write(w, apierr.AIProposalClosed, "Já existe um registo com este identificador.", "logId")
	default:
		apierr.WriteInternal(w, r, err, "Não foi possível concluir o pedido.")
	}
}
