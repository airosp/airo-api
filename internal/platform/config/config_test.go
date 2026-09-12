package config_test

import (
	"strings"
	"testing"

	"github.com/airosp/airo-api/internal/platform/config"
)

// Um segredo curto não é meio segredo. Onze caracteres estiveram em produção e
// a API subiu sem uma única rota privada — o assinador exige 32 bytes, não os
// tinha, e as rotas simplesmente não se registaram. Ninguém viu, porque nada
// falhou: o `/healthz` respondia 200 e tudo o resto dava 404.
func TestSegredoCurtoRecusaArrancar(t *testing.T) {
	base := map[string]string{
		"AIRO_ENV":          "production",
		"AIRO_DATABASE_URL": "postgres://u:p@h:5432/d",
		"AIRO_REDIS_URL":    "redis://h:6379",
		"AIRO_OTP_PEPPER":   strings.Repeat("p", 32),
		"AIRO_JWT_SECRET":   strings.Repeat("s", 32),
	}

	casos := []struct {
		nome  string
		chave string
		valor string
		erro  string
	}{
		{"jwt de 11 caracteres", "AIRO_JWT_SECRET", "curto-11-ca", "AIRO_JWT_SECRET"},
		{"jwt de 31 bytes", "AIRO_JWT_SECRET", strings.Repeat("s", 31), "AIRO_JWT_SECRET"},
		{"pepper de 31 bytes", "AIRO_OTP_PEPPER", strings.Repeat("p", 31), "AIRO_OTP_PEPPER"},
		{"jwt vazio", "AIRO_JWT_SECRET", "", "AIRO_JWT_SECRET"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			for k, v := range base {
				t.Setenv(k, v)
			}
			t.Setenv(c.chave, c.valor)

			_, err := config.Load()
			if err == nil {
				t.Fatalf("%s com %q devia recusar o arranque", c.chave, c.valor)
			}
			if !strings.Contains(err.Error(), c.erro) {
				t.Fatalf("o erro tem de dizer qual a variável; disse: %v", err)
			}
		})
	}
}

// E com segredos de tamanho suficiente arranca, senão o teste acima passaria
// com uma configuração que recusa tudo.
func TestSegredosSuficientesArrancam(t *testing.T) {
	t.Setenv("AIRO_ENV", "production")
	t.Setenv("AIRO_DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("AIRO_REDIS_URL", "redis://h:6379")
	t.Setenv("AIRO_OTP_PEPPER", strings.Repeat("p", 32))
	t.Setenv("AIRO_JWT_SECRET", strings.Repeat("s", 64))

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProduction() {
		t.Fatal("AIRO_ENV=production devia dar IsProduction()")
	}
}

// Em desenvolvimento não se exige nada disto: quem corre a API na própria
// máquina não devia ter de inventar segredos para ver um ecrã.
func TestDesenvolvimentoNaoExigeSegredos(t *testing.T) {
	t.Setenv("AIRO_ENV", "development")
	t.Setenv("AIRO_DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("AIRO_OTP_PEPPER", "")
	t.Setenv("AIRO_JWT_SECRET", "")

	if _, err := config.Load(); err != nil {
		t.Fatalf("desenvolvimento devia arrancar sem segredos: %v", err)
	}
}
