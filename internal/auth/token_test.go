package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func issuer(t *testing.T) (*TokenIssuer, *time.Time) {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	return NewTokenIssuer(secret, func() time.Time { return now }), &now
}

func TestIssueAndVerify(t *testing.T) {
	iss, _ := issuer(t)
	token, expires, err := iss.Issue("user-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if expires.Sub(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)) != AccessTTL {
		t.Fatalf("expira em %v", expires)
	}
	got, err := iss.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if got != "user-1" {
		t.Fatalf("sub = %q", got)
	}
}

func TestExpiredTokenIsRefused(t *testing.T) {
	secret := make([]byte, 32)
	rand.Read(secret)
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	iss := NewTokenIssuer(secret, func() time.Time { return now })

	token, _, _ := iss.Issue("user-1", "d")

	later := NewTokenIssuer(secret, func() time.Time { return now.Add(16 * time.Minute) })
	if _, err := later.Verify(context.Background(), token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("%v", err)
	}
}

// ⚠️ O defeito clássico do JWT: aceitar o algoritmo que o **token** declara.
//
// Um token com `alg: none` passa sem assinatura nenhuma. Quem verifica é que
// escolhe o algoritmo — e há um teste que o tenta.
func TestAlgNoneIsRefused(t *testing.T) {
	iss, _ := issuer(t)

	claims := jwt.MapClaims{
		"sub": "atacante", "iss": "airo",
		"exp": time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC).Unix(),
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Verify(context.Background(), unsigned); err == nil {
		t.Fatal("um token sem assinatura foi aceite")
	}
}

// Outro segredo, outro token: assinado por quem não devia, não entra.
func TestForeignSignatureIsRefused(t *testing.T) {
	iss, _ := issuer(t)

	other := make([]byte, 32)
	rand.Read(other)
	forged, _, err := NewTokenIssuer(other, func() time.Time {
		return time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	}).Issue("atacante", "d")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Verify(context.Background(), forged); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("%v", err)
	}
}

func TestGarbageIsRefused(t *testing.T) {
	iss, _ := issuer(t)
	for _, raw := range []string{"", "abc", "a.b.c", strings.Repeat("x", 500)} {
		if _, err := iss.Verify(context.Background(), raw); err == nil {
			t.Errorf("%q foi aceite", raw)
		}
	}
}

func TestShortSecretIsRefused(t *testing.T) {
	iss := NewTokenIssuer([]byte("curto"), time.Now)
	if _, _, err := iss.Issue("u", "d"); err == nil {
		t.Fatal("um segredo curto devia ser recusado ao emitir, não ignorado")
	}
}
