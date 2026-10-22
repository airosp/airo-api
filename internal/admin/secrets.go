package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinPassword é o comprimento mínimo. Comprido em vez de complicado: dez
// caracteres quaisquer custam mais a adivinhar do que oito com um símbolo.
const MinPassword = 10

// HashPassword usa bcrypt, que é lento de propósito.
func HashPassword(p string) (string, error) {
	if len(p) < MinPassword {
		return "", fmt.Errorf("palavra-passe curta")
	}
	if len(p) > 72 {
		// O bcrypt ignora o que passa de 72 bytes; recusar é mais honesto do
		// que aceitar uma parte.
		return "", fmt.Errorf("palavra-passe longa de mais")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), 12)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword compara em tempo constante (o bcrypt já o faz).
func CheckPassword(hash, p string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

// DummyCheck gasta o mesmo tempo que uma verificação a sério, para um email
// que não existe não responder mais depressa do que um que existe.
func DummyCheck(p string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(p))
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("airo-painel-dummy"), 12)

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

// Sealer cifra os segredos da app autenticadora com AES-GCM.
//
// A chave deriva do segredo dos tokens da API, com um rótulo próprio: não há
// uma variável nova para configurar, e a chave dos JWT nunca é usada como tal.
type Sealer struct{ aead cipher.AEAD }

func NewSealer(master []byte) (*Sealer, error) {
	if len(master) < 32 {
		return nil, errors.New("segredo mestre curto")
	}
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("airo-admin-totp-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("segredo cifrado ilegível")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
