package auth

import (
	"errors"
	"strings"
	"testing"
)

// O mesmo número, escrito de todas as maneiras que alguém o escreve, tem de dar
// a mesma conta. Sem isto, quem escreveu com espaços perde o histórico de quem
// escreveu sem.
func TestNormalizeCollapsesEveryWayOfWritingIt(t *testing.T) {
	same := []string{
		"84 123 4567",
		"841234567",
		"+258 84 123 4567",
		"+258841234567",
		"00258841234567",
		"(84) 123-4567",
		"  84 123 4567  ",
	}
	const want = "+258841234567"

	for _, raw := range same {
		got, region, err := NormalizePhone(raw, "MZ")
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, queria %q", raw, got, want)
		}
		if region != "MZ" {
			t.Errorf("%q → região %q", raw, region)
		}
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"", "123", "abc", "+258 1", "84123456789012345", "+1"} {
		if _, _, err := NormalizePhone(raw, "MZ"); !errors.Is(err, ErrInvalidPhone) {
			t.Errorf("%q devia ser recusado, deu %v", raw, err)
		}
	}
}

// Um número de outro país funciona: a região vem do número, não do valor por
// omissão. Quem está fora de Moçambique também entra.
func TestForeignNumbersKeepTheirRegion(t *testing.T) {
	cases := map[string]struct{ e164, region string }{
		"+351912345678":     {"+351912345678", "PT"},
		"+55 11 91234-5678": {"+5511912345678", "BR"},
		"+27 82 123 4567":   {"+27821234567", "ZA"},
	}
	for raw, want := range cases {
		got, region, err := NormalizePhone(raw, "MZ")
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if got != want.e164 || region != want.region {
			t.Errorf("%q → %q/%q, queria %q/%q", raw, got, region, want.e164, want.region)
		}
	}
}

// O E.164 do esquema tem um CHECK: `^\+[1-9][0-9]{7,14}$`. O que sai daqui tem
// de o satisfazer sempre, senão a gravação falha depois de o código já ter sido
// enviado — e a pessoa paga o SMS para receber um erro.
func TestOutputMatchesTheSchemaConstraint(t *testing.T) {
	for _, raw := range []string{"841234567", "+351912345678", "+5511912345678", "+27821234567"} {
		got, _, err := NormalizePhone(raw, "MZ")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) < 9 || len(got) > 16 || got[0] != '+' || got[1] == '0' {
			t.Errorf("%q → %q não satisfaz o CHECK do esquema", raw, got)
		}
		for _, r := range got[1:] {
			if r < '0' || r > '9' {
				t.Errorf("%q → %q tem caracteres que não são dígitos", raw, got)
				break
			}
		}
	}
}

func TestMaskKeepsEnoughToInvestigateAndNotEnoughToIdentify(t *testing.T) {
	got := MaskPhone("+258841234567")
	if got == "+258841234567" {
		t.Fatal("não mascarou nada")
	}
	if got[:4] != "+258" {
		t.Errorf("%q — o indicativo devia ficar", got)
	}
	if got[len(got)-2:] != "67" {
		t.Errorf("%q — os dois últimos dígitos deviam ficar", got)
	}
	// E não pode dar para reconstruir.
	digits := 0
	for _, r := range got {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits > 6 {
		t.Errorf("%q deixa %d dígitos à vista", got, digits)
	}
	if MaskPhone("123") != "[oculto]" {
		t.Error("um número curto demais é ocultado por inteiro")
	}
}

// T6.10 — o IP nunca é guardado em claro.
//
// Para contar pedidos por origem, o hash chega. O IP em claro é dado pessoal e
// não tem de existir em lado nenhum — nem na base de dados, nem nos contadores
// de limite, nem nos registos.
func TestOIPNuncaFicaEmClaro(t *testing.T) {
	ips := []string{"41.94.12.7", "2001:db8::1", "192.168.1.1"}
	for _, ip := range ips {
		h := HashIP(ip)
		if h == "" {
			t.Errorf("HashIP(%q) devolveu vazio", ip)
			continue
		}
		if strings.Contains(h, ip) {
			t.Errorf("o IP %q aparece no hash %q", ip, h)
		}
		// Estável: o mesmo IP tem de dar o mesmo contador, senão o limite por
		// origem não limita nada.
		if HashIP(ip) != h {
			t.Errorf("HashIP(%q) não é estável", ip)
		}
	}

	// IPs diferentes não podem colidir — colidir seria limitar a pessoa errada.
	if HashIP(ips[0]) == HashIP(ips[2]) {
		t.Error("dois IPs diferentes deram o mesmo hash")
	}
	// Sem IP não se inventa um.
	if HashIP("") != "" {
		t.Error("sem IP devia devolver vazio")
	}
}
