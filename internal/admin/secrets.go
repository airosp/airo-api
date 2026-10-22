package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewToken devolve um token opaco para o browser e o hash que se guarda. A base
// de dados nunca vê o token: quem a ler não entra no painel.
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken é SHA-256: o token já tem 256 bits de acaso, não precisa de sal.
func HashToken(token string) []byte {
	s := sha256.Sum256([]byte(token))
	return s[:]
}
