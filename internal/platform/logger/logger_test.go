package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func capture(t *testing.T, fn func(*slog.Logger)) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug, ReplaceAttr: scrub,
	}))
	fn(log)
	return buf.String()
}

// ⚠️ O código OTP nunca aparece em registos, traces ou relatórios de erro.
//
// É um ponto da lista de verificação de docs/backend/08-autenticacao.md §14, e a
// única forma de o garantir é um teste: basta um `log.Info("otp", "code", code)`
// escrito à pressa para os códigos activos de toda a gente ficarem no ficheiro.
func TestSecretsNeverReachTheLogs(t *testing.T) {
	secrets := map[string]string{
		"code":          "483920",
		"otp":           "483920",
		"otp_code":      "483920",
		"password":      "segredo",
		"token":         "eyJhbGciOiJI",
		"access_token":  "eyJhbGciOiJI",
		"refresh_token": "opaco-base64url",
		"authorization": "Bearer eyJhbGciOiJI",
		"pepper":        "ABCDEF0123456789",
		"secret":        "ABCDEF0123456789",
	}

	for field, value := range secrets {
		out := capture(t, func(log *slog.Logger) {
			log.Info("um pedido qualquer", field, value)
		})
		if strings.Contains(out, value) {
			t.Errorf("o campo %q escreveu %q nos registos: %s", field, value, out)
		}
		if !strings.Contains(out, "[oculto]") {
			t.Errorf("o campo %q não foi ocultado: %s", field, out)
		}
	}
}

// Maiúsculas e minúsculas não escapam ao filtro.
func TestScrubIsCaseInsensitive(t *testing.T) {
	for _, field := range []string{"Code", "CODE", "Refresh_Token", "AUTHORIZATION"} {
		out := capture(t, func(log *slog.Logger) { log.Info("x", field, "483920") })
		if strings.Contains(out, "483920") {
			t.Errorf("%q escapou: %s", field, out)
		}
	}
}

// O que não é segredo continua a ser registado — senão o filtro seria inútil
// para investigar seja o que for.
func TestNonSecretsAreKept(t *testing.T) {
	out := capture(t, func(log *slog.Logger) {
		log.Info("pedido", "path", "/v1/goals", "status", 201, "ms", 42)
	})
	for _, want := range []string{"/v1/goals", "201", "42"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q devia estar nos registos: %s", want, out)
		}
	}
}

// O número de telefone é dado pessoal: vai mascarado, e o que resta chega para
// investigar sem chegar para identificar.
func TestPhoneIsMasked(t *testing.T) {
	got := Phone("+258841234567")
	if got == "+258841234567" {
		t.Fatal("não mascarou")
	}
	if !strings.HasPrefix(got, "+258") || !strings.HasSuffix(got, "67") {
		t.Errorf("%q — devia manter o indicativo e os dois últimos", got)
	}
	if Phone("123") != "[oculto]" {
		t.Error("um número curto demais é ocultado por inteiro")
	}
}
