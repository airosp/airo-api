// Package admin guarda as regras de entrada e de acesso do painel da equipa.
//
// Sem I/O, como os motores: recebe segredos e instantes, devolve decisões. É o
// que permite testar a janela do código ou a matriz de permissões sem base de
// dados.
package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238: as apps autenticadoras usam SHA-1.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// O passo e os dígitos que todas as apps autenticadoras assumem por omissão.
const (
	totpStep   = 30 * time.Second
	totpDigits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret gera os 20 bytes que a RFC 4226 recomenda.
func NewTOTPSecret() ([]byte, error) {
	s := make([]byte, 20)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("gerar segredo: %w", err)
	}
	return s, nil
}

// SecretText é o segredo como se escreve à mão na app, em base32.
func SecretText(secret []byte) string { return b32.EncodeToString(secret) }

// OTPAuthURI é o que o QR leva.
func OTPAuthURI(secret []byte, email string) string {
	label := url.PathEscape("Airo Equipa:" + email)
	q := url.Values{}
	q.Set("secret", SecretText(secret))
	q.Set("issuer", "Airo Equipa")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func codeAt(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", bin%1_000_000)
}

// TOTPCode é o código válido no instante `at`. Os testes usam-no; a entrada usa
// `VerifyTOTP`.
func TOTPCode(secret []byte, at time.Time) string {
	return codeAt(secret, uint64(at.Unix()/int64(totpStep.Seconds())))
}

// VerifyTOTP aceita o passo actual e o vizinho de cada lado: um relógio de
// telemóvel trinta segundos adiantado não é um ataque.
func VerifyTOTP(secret []byte, code string, at time.Time) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return false
	}
	now := uint64(at.Unix() / int64(totpStep.Seconds()))
	ok := 0
	for _, c := range []uint64{now - 1, now, now + 1} {
		ok |= subtle.ConstantTimeCompare([]byte(codeAt(secret, c)), []byte(code))
	}
	return ok == 1
}
