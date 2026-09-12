package service

import "errors"

// Erros de domínio do serviço. O transporte traduz cada um para o seu código e
// estado HTTP — ver docs/backend/05-api.md.
var (
	// ErrHorizonMismatch → 422 `journey_horizon_mismatch`
	ErrHorizonMismatch = errors.New("horizonte incompatível com a data-alvo")
	// ErrInsufficientData → 409 `insufficient_data`
	ErrInsufficientData = errors.New("dados insuficientes para avaliar")
)
