// Package auth é a identidade: telefone, código, sessão.
//
// É o único sítio do sistema onde um erro custa contas de utilizadores — e por
// isso quase tudo aqui é escolhido pela propriedade que garante, não pela
// conveniência.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/nyaruka/phonenumbers"
)

// ErrInvalidPhone corresponde ao código `invalid_phone` da API.
var ErrInvalidPhone = errors.New("número de telefone inválido")

// DefaultRegion é Moçambique. O mercado inicial escreve o número sem indicativo.
const DefaultRegion = "MZ"

// NormalizePhone devolve o número em E.164 e a região.
//
// **Tudo passa por aqui antes de qualquer decisão.** Sem normalização, o mesmo
// número cria contas diferentes conforme como foi escrito — e a pessoa perde o
// histórico por ter escrito `84 123 4567` em vez de `+258841234567`.
//
// A forma como o utilizador o escreveu não se guarda: a coluna única é o E.164.
func NormalizePhone(raw, defaultRegion string) (e164 string, region string, err error) {
	if defaultRegion == "" {
		defaultRegion = DefaultRegion
	}
	num, err := phonenumbers.Parse(raw, defaultRegion)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidPhone, err)
	}
	if !phonenumbers.IsValidNumber(num) {
		return "", "", ErrInvalidPhone
	}

	e164 = phonenumbers.Format(num, phonenumbers.E164)
	region = phonenumbers.GetRegionCodeForNumber(num)
	if region == "" {
		region = defaultRegion
	}
	return e164, region, nil
}

// MaskPhone encurta um número para registo: mantém o indicativo e os dois
// últimos dígitos.
//
// Suficiente para investigar um incidente, insuficiente para identificar alguém
// a partir dos registos.
func MaskPhone(e164 string) string {
	if len(e164) < 7 {
		return "[oculto]"
	}
	masked := make([]rune, 0, len(e164))
	runes := []rune(e164)
	for i, r := range runes {
		switch {
		case i < 4 || i >= len(runes)-2:
			masked = append(masked, r)
		default:
			masked = append(masked, '•')
		}
	}
	return string(masked)
}

// HashIP reduz um IP a hash.
//
// O IP em claro é dado pessoal e não precisa de existir: para contar pedidos por
// origem, o hash chega — e uma fuga dos registos não entrega a morada de rede de
// ninguém.
func HashIP(ip string) string {
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("airo-ip:" + ip))
	return hex.EncodeToString(sum[:16])
}
