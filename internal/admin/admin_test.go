package admin

import (
	"encoding/hex"
	"testing"
	"time"
)

// O vector da RFC 6238, apêndice B, cortado aos seis dígitos.
func TestTOTPRFC(t *testing.T) {
	secret := []byte("12345678901234567890")
	at := time.Unix(59, 0)
	if got := TOTPCode(secret, at); got != "287082" {
		t.Fatalf("código = %s, quero 287082", got)
	}
	if !VerifyTOTP(secret, "287082", at.Add(30*time.Second)) {
		t.Fatal("o passo anterior tem de valer")
	}
	if VerifyTOTP(secret, "287082", at.Add(90*time.Second)) {
		t.Fatal("dois passos depois já não vale")
	}
	if VerifyTOTP(secret, "28708", at) {
		t.Fatal("cinco dígitos não é um código")
	}
}

func TestSealer(t *testing.T) {
	s, err := NewSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Seal([]byte("segredo"))
	p, err := s.Open(c)
	if err != nil || string(p) != "segredo" {
		t.Fatalf("abrir: %v %q", err, p)
	}
	c[len(c)-1] ^= 1
	if _, err := s.Open(c); err == nil {
		t.Fatal("um segredo adulterado tem de falhar")
	}
	_ = hex.EncodeToString
}

func TestMatrix(t *testing.T) {
	if !Can(RoleAdmin, AreaTeam, Edit) || Can(RoleContent, AreaTeam, View) {
		t.Fatal("só a administração gere a equipa")
	}
	for _, r := range Roles {
		if Can(r, AreaUserRecord, Edit) {
			t.Fatalf("%s não pode editar a ficha de saúde", r)
		}
	}
	if Can(RoleSupport, AreaClasses, Edit) || !Can(RoleSupport, AreaClasses, View) {
		t.Fatal("o suporte vê as aulas, não as muda")
	}
}

func TestPassword(t *testing.T) {
	if _, err := HashPassword("curta"); err == nil {
		t.Fatal("curta tem de falhar")
	}
	h, err := HashPassword("uma frase comprida")
	if err != nil || !CheckPassword(h, "uma frase comprida") || CheckPassword(h, "outra") {
		t.Fatal("bcrypt")
	}
}
