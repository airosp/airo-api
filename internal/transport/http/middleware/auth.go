package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

type userKey struct{}

// TokenVerifier valida um access token e devolve o utilizador.
//
// Interface e não implementação: a verificação real chega com a Fase 3
// (telefone + OTP). Até lá, o encaminhamento já está protegido e é só trocar
// quem o implementa — em vez de acrescentar autenticação a handlers que
// nasceram sem ela.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (userID string, err error)
}

// Auth exige um access token válido.
func Auth(v TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
				return
			}
			userID, err := v.Verify(r.Context(), token)
			if err != nil || userID == "" {
				// A mensagem é a mesma para token inválido e expirado: distinguir
				// diz ao atacante se o token existiu alguma vez.
				apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, userID)))
		})
	}
}

// UserID devolve o utilizador autenticado.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userKey{}).(string)
	return id, ok && id != ""
}
