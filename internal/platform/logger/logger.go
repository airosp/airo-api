// Package logger dá um registo estruturado com um cuidado que não é opcional:
// há valores que nunca podem sair daqui.
//
// O código OTP, o token de refresh e o número de telefone em claro não aparecem
// em registos, traces nem relatórios de erro. `Redact` existe para tornar isso
// fácil de fazer bem e difícil de fazer mal.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New devolve um registador JSON em produção e legível em desenvolvimento.
func New(env string, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: scrub}
	if env == "development" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// Campos que nunca podem ser escritos, mesmo que alguém os passe por engano.
// A defesa é aqui e não em cada chamada: basta um esquecimento.
var forbidden = map[string]bool{
	"code": true, "otp": true, "otp_code": true, "code_hash": true,
	"password": true, "token": true, "access_token": true, "refresh_token": true,
	"authorization": true, "pepper": true, "secret": true,
}

func scrub(_ []string, a slog.Attr) slog.Attr {
	if forbidden[strings.ToLower(a.Key)] {
		return slog.String(a.Key, "[oculto]")
	}
	return a
}

// Phone encurta um número para registo: mantém o país e os dois últimos
// dígitos. Suficiente para investigar um incidente, insuficiente para
// identificar alguém a partir dos registos.
func Phone(e164 string) string {
	if len(e164) < 6 {
		return "[oculto]"
	}
	return e164[:4] + strings.Repeat("•", len(e164)-6) + e164[len(e164)-2:]
}
