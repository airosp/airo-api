package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTTL — quinze minutos, e vive **em memória** no cliente.
//
// Curto porque não é revogável: a revogação faz-se no refresh, que é opaco e
// tem tabela. Um access token de horas seria uma janela de horas para quem o
// apanhasse.
const AccessTTL = 15 * time.Minute

var (
	ErrTokenInvalid = errors.New("token inválido")
	ErrTokenExpired = errors.New("token expirado")
)

type TokenIssuer struct {
	secret []byte
	now    func() time.Time
}

func NewTokenIssuer(secret []byte, now func() time.Time) *TokenIssuer {
	if now == nil {
		now = time.Now
	}
	return &TokenIssuer{secret: secret, now: now}
}

// Issue emite o access token.
func (t *TokenIssuer) Issue(userID, deviceID string) (string, time.Time, error) {
	if len(t.secret) < 32 {
		return "", time.Time{}, errors.New("segredo de assinatura curto demais")
	}
	now := t.now()
	expires := now.Add(AccessTTL)

	claims := jwt.MapClaims{
		"sub": userID,
		"dev": deviceID,
		"iat": now.Unix(),
		"exp": expires.Unix(),
		"iss": "airo",
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("assinar token: %w", err)
	}
	return signed, expires, nil
}

// Verify valida o token e devolve o utilizador.
//
// ⚠️ O método de assinatura é **fixado**, não lido do token.
//
// Aceitar o `alg` que o token declara é o defeito clássico do JWT: um token com
// `alg: none` passa sem assinatura nenhuma, e um com `alg: HS256` assinado com a
// chave pública RSA passa noutra variante. Quem verifica escolhe o algoritmo.
func (t *TokenIssuer) Verify(_ context.Context, raw string) (string, error) {
	parsed, err := jwt.Parse(raw, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("%w: método %v", ErrTokenInvalid, token.Header["alg"])
		}
		return t.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("airo"),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", ErrTokenExpired
		}
		return "", fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", ErrTokenInvalid
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", ErrTokenInvalid
	}
	return sub, nil
}
