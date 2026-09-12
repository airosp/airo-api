package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

type Auth struct {
	Service *service.AuthService
	Tokens  *auth.TokenIssuer
}

func (h Auth) RequestOTP(w http.ResponseWriter, r *http.Request) {
	var req dto.OTPRequestRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	out, err := h.Service.RequestOTP(r.Context(), service.RequestOTPInput{
		RawPhone: req.Phone, Channel: req.Channel,
		DeviceID: req.DeviceID, IPHash: clientIPHash(r),
	})
	switch {
	case errors.Is(err, auth.ErrInvalidPhone):
		apierr.Write(w, apierr.InvalidPhone, "Número de telefone inválido.", "phone")
		return
	case errors.Is(err, service.ErrRateLimited):
		// ⚠️ A resposta **não diz qual** limite bateu: isso diria ao atacante
		// exactamente o que contornar.
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfterOf(err).Seconds())))
		apierr.Write(w, apierr.RateLimited, "Demasiados pedidos. Tenta daqui a pouco.", "")
		return
	case errors.Is(err, service.ErrUndeliverable):
		// Sem contrato de SMS ainda, a alternativa honesta é dizer o que se
		// passa — não oferecer um canal que não existe. Ver
		// docs/backend/08-autenticacao.md §7.
		apierr.Write(w, apierr.DeliveryFailed,
			"Este número não tem WhatsApp. Tenta com outro número.", "phone")
		return
	case errors.Is(err, service.ErrDeliveryFailed):
		apierr.Write(w, apierr.DeliveryFailed,
			"Não conseguimos enviar o código agora. Tenta daqui a pouco.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível enviar o código.")
		return
	}

	// ⚠️ 202, e a resposta é **exactamente a mesma** para número registado e
	// desconhecido. O `isNewUser` só aparece depois de a pessoa provar que
	// controla o número.
	apierr.WriteJSON(w, http.StatusAccepted, dto.OTPRequestResponse{
		ChallengeID: out.ChallengeID,
		ExpiresAt:   out.ExpiresAt.UTC().Format(time.RFC3339),
		ResendAfter: out.ResendAfter,
	})
}

func (h Auth) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req dto.OTPVerifyRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	if !req.PlatformOK() {
		apierr.Write(w, apierr.ValidationFailed,
			"Plataforma desconhecida. Usa ios, android ou web.", "platform")
		return
	}

	out, err := h.Service.VerifyOTP(r.Context(), service.VerifyOTPInput{
		ChallengeID: req.ChallengeID, Code: req.Code,
		DeviceID: req.DeviceID, Platform: req.Platform,
	})
	switch {
	case errors.Is(err, service.ErrOTPExpired):
		apierr.Write(w, apierr.OTPExpired, "O código expirou. Pede um novo.", "")
		return
	case errors.Is(err, service.ErrOTPExhausted):
		apierr.Write(w, apierr.OTPExhausted, "Demasiadas tentativas. Pede um código novo.", "")
		return
	case errors.Is(err, service.ErrOTPInvalid):
		// ⚠️ **Não diz quantas tentativas faltam.** "Faltam 2" diz ao atacante
		// exactamente quanto orçamento lhe resta.
		apierr.Write(w, apierr.OTPInvalid, "Código incorreto.", "code")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível verificar o código.")
		return
	}

	access, expires, err := h.Tokens.Issue(out.UserID, req.DeviceID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível abrir a sessão.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, dto.SessionResponse{
		AccessToken:  access,
		RefreshToken: out.RefreshToken,
		ExpiresIn:    int(time.Until(expires).Seconds()),
		IsNewUser:    out.IsNewUser,
	})
}

func (h Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	var req dto.RefreshRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	out, err := h.Service.Refresh(r.Context(), req.RefreshToken, req.DeviceID)
	switch {
	case errors.Is(err, repo.ErrTokenReuse):
		// A família caiu. A pessoa volta a entrar — e é dito porquê, porque uma
		// sessão que termina sem explicação parece uma avaria.
		apierr.Write(w, apierr.TokenReuseDetected,
			"A sessão foi terminada por segurança. Entra outra vez.", "")
		return
	case errors.Is(err, repo.ErrTokenNotFound):
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível renovar a sessão.")
		return
	}

	access, expires, err := h.Tokens.Issue(out.UserID, req.DeviceID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível renovar a sessão.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, dto.SessionResponse{
		AccessToken:  access,
		RefreshToken: out.RefreshToken,
		ExpiresIn:    int(time.Until(expires).Seconds()),
	})
}

// clientIPHash devolve o IP já reduzido a hash.
//
// O IP em claro é dado pessoal e não tem de existir em lado nenhum: para contar
// pedidos por origem, o hash chega.
func clientIPHash(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if i := strings.IndexByte(ip, ','); i > 0 {
		ip = ip[:i]
	}
	if ip == "" {
		ip = r.RemoteAddr
		if i := strings.LastIndexByte(ip, ':'); i > 0 {
			ip = ip[:i]
		}
	}
	if ip == "" {
		return ""
	}
	return auth.HashIP(strings.TrimSpace(ip))
}

func retryAfterOf(err error) time.Duration {
	// A duração vem no texto do erro do serviço; sem ela, uma hora é o valor
	// conservador.
	if err == nil {
		return time.Hour
	}
	if i := strings.LastIndex(err.Error(), ": "); i > 0 {
		if d, parseErr := time.ParseDuration(err.Error()[i+2:]); parseErr == nil {
			return d
		}
	}
	return time.Hour
}

var _ = middleware.UserID
