package apierr

import "net/http"

// Códigos da camada de inteligência. Cada um leva a app a fazer uma coisa
// diferente, e por isso nenhum é o genérico.
const (
	// AIUnavailable: desligada, sem orçamento ou em pausa. A app mostra o
	// registo manual — é o recurso, não um erro.
	AIUnavailable Code = "ai_unavailable"
	// AIConsentRequired: falta a autorização. A app mostra o texto e pede-a.
	AIConsentRequired Code = "ai_consent_required"
	// AIDailyLimit: a pessoa chegou ao limite do dia.
	AIDailyLimit Code = "ai_daily_limit"
	// AIProposalStale: o rascunho mudou desde que foi lido. Lê-se outra vez.
	AIProposalStale Code = "ai_proposal_stale"
	// AIProposalClosed: o rascunho já foi confirmado, dispensado ou expirou.
	AIProposalClosed Code = "ai_proposal_closed"
)

func init() {
	status[AIUnavailable] = http.StatusServiceUnavailable
	status[AIConsentRequired] = http.StatusForbidden
	status[AIDailyLimit] = http.StatusTooManyRequests
	status[AIProposalStale] = http.StatusConflict
	status[AIProposalClosed] = http.StatusConflict
}
