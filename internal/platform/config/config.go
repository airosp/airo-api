// Package config lê a configuração do ambiente.
//
// Duas categorias, tratadas de maneiras diferentes:
//
//   - **Infra-estrutura** (endereços, segredos) vem do ambiente e é obrigatória
//     em produção. Faltar é erro de arranque, não valor por omissão silencioso.
//   - **Limiares de domínio** (kcal, séries, descansos) NÃO vivem aqui. São
//     dados carregados em tempo de execução, porque não estão validados
//     clinicamente e vão mudar sem nova versão da aplicação — ver
//     docs/backend/04-arquitetura-go.md.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Env string

const (
	Development Env = "development"
	Staging     Env = "staging"
	Production  Env = "production"
)

type Config struct {
	Env         Env
	HTTPAddr    string
	DatabaseURL string
	RedisURL    string

	// OTPPepper tempera o HMAC dos códigos. Vive fora da base de dados: uma
	// fuga só da BD deixa os códigos guardados inúteis.
	OTPPepper []byte
	JWTSecret []byte

	WhatsApp WhatsAppConfig
}

type WhatsAppConfig struct {
	PhoneNumberID string
	Token         string
	Template      string
	// Language é o código de língua do template aprovado. `pt_PT` e `pt_BR`
	// são templates diferentes para a Meta, não variantes do mesmo.
	Language      string
	WebhookSecret string
}

func (c Config) IsProduction() bool { return c.Env == Production }

// Load lê o ambiente. Em produção exige os segredos; em desenvolvimento
// aceita a falta deles para a API arrancar sem WhatsApp configurado.
func Load() (Config, error) {
	c := Config{
		Env:         Env(get("AIRO_ENV", string(Development))),
		HTTPAddr:    get("AIRO_HTTP_ADDR", ":8080"),
		DatabaseURL: get("AIRO_DATABASE_URL", ""),
		RedisURL:    get("AIRO_REDIS_URL", ""),
		OTPPepper:   []byte(get("AIRO_OTP_PEPPER", "")),
		JWTSecret:   []byte(get("AIRO_JWT_SECRET", "")),
		WhatsApp: WhatsAppConfig{
			PhoneNumberID: get("AIRO_WHATSAPP_PHONE_NUMBER_ID", ""),
			Token:         get("AIRO_WHATSAPP_TOKEN", ""),
			Template:      get("AIRO_WHATSAPP_TEMPLATE", "airo_otp"),
			Language:      get("AIRO_WHATSAPP_LANGUAGE", "pt_PT"),
			WebhookSecret: get("AIRO_WHATSAPP_WEBHOOK_SECRET", ""),
		},
	}

	switch c.Env {
	case Development, Staging, Production:
	default:
		return c, fmt.Errorf("AIRO_ENV inválido: %q", c.Env)
	}

	var missing []string
	required := map[string]bool{"AIRO_DATABASE_URL": c.DatabaseURL == ""}
	if c.Env != Development {
		// Fora de desenvolvimento, arrancar sem pepper ou sem segredo de
		// assinatura é pior do que não arrancar: a API subiria a emitir tokens
		// assinados com nada.
		required["AIRO_REDIS_URL"] = c.RedisURL == ""
		required["AIRO_OTP_PEPPER"] = len(c.OTPPepper) == 0
		required["AIRO_JWT_SECRET"] = len(c.JWTSecret) == 0
	}
	for name, isMissing := range required {
		if isMissing {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("configuração em falta: %s", strings.Join(missing, ", "))
	}
	// O comprimento é verificado, não só a presença. Um segredo de onze
	// caracteres passou por aqui e a API subiu em produção sem uma única rota
	// privada: o `Wire` exige 32 bytes para assinar, não os tinha, e desistiu
	// com um aviso. Recusar à cabeça troca esse silêncio por uma paragem que se
	// lê no arranque.
	if c.Env != Development {
		if len(c.OTPPepper) < 32 {
			return c, errors.New("AIRO_OTP_PEPPER tem de ter pelo menos 32 bytes")
		}
		if len(c.JWTSecret) < 32 {
			return c, fmt.Errorf(
				"AIRO_JWT_SECRET tem de ter pelo menos 32 bytes (tem %d)", len(c.JWTSecret))
		}
	}
	return c, nil
}

func get(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
