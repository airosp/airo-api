// Package apierr é o formato de erro da API.
//
// Um erro é uma resposta como outra qualquer: tem de ser previsível, tem de
// dizer **qual** campo falhou, e tem de ter um código estável que o cliente
// possa comparar. Uma mensagem em texto livre obriga o cliente a compará-la —
// e aí muda-se a redacção e parte-se a app.
package apierr

import (
	"encoding/json"
	"net/http"
)

type Code string

// Os códigos de docs/backend/05-api.md. Estáveis: a mensagem muda, o código não.
const (
	ValidationFailed       Code = "validation_failed"
	HorizonMismatch        Code = "journey_horizon_mismatch"
	GoalAlreadyActive      Code = "goal_already_active"
	InsufficientData       Code = "insufficient_data"
	SessionAlreadyRecorded Code = "session_already_recorded"
	RateLimited            Code = "rate_limited"
	Unauthorized           Code = "unauthorized"
	NotFound               Code = "not_found"
	Internal               Code = "internal"
)

type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Field aponta o campo do pedido que falhou, quando há um.
	Field string `json:"field,omitempty"`
}

type envelope struct {
	Error Error `json:"error"`
}

var status = map[Code]int{
	ValidationFailed:  http.StatusUnprocessableEntity,
	HorizonMismatch:   http.StatusUnprocessableEntity,
	GoalAlreadyActive: http.StatusConflict,
	InsufficientData:  http.StatusConflict,
	RateLimited:       http.StatusTooManyRequests,
	Unauthorized:      http.StatusUnauthorized,
	NotFound:          http.StatusNotFound,
	Internal:          http.StatusInternalServerError,
}

// StatusFor devolve o estado HTTP de um código. Um código desconhecido é 500 —
// nunca 200, que esconderia o erro.
func StatusFor(c Code) int {
	if s, ok := status[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Write escreve o erro. É o único sítio que o faz, para o formato não divergir.
func Write(w http.ResponseWriter, c Code, message, field string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(StatusFor(c))
	_ = json.NewEncoder(w).Encode(envelope{Error{Code: c, Message: message, Field: field}})
}

// WriteJSON escreve uma resposta com sucesso.
func WriteJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}
