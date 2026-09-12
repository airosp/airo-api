package auth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func pepper(t *testing.T) []byte {
	t.Helper()
	p := make([]byte, 32)
	if _, err := rand.Read(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCodeShape(t *testing.T) {
	for i := 0; i < 1000; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != CodeLength {
			t.Fatalf("%q tem %d dígitos", code, len(code))
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("%q não é só dígitos", code)
			}
		}
	}
}

// Um gerador previsível é o mesmo que não ter código nenhum. Não se prova
// aleatoriedade com um teste, mas prova-se que não é constante nem sequencial, e
// que a distribuição não colapsa num canto.
func TestCodeIsNotPredictable(t *testing.T) {
	const n = 20000
	seen := map[string]int{}
	buckets := [10]int{}

	for i := 0; i < n; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		seen[code]++
		buckets[code[0]-'0']++
	}

	// Com 20 000 códigos de um milhão, repetições são normais; muitas não são.
	if len(seen) < n*95/100 {
		t.Fatalf("só %d códigos distintos em %d", len(seen), n)
	}
	// O primeiro dígito tem de cobrir os dez valores de forma parecida.
	for d, count := range buckets {
		if count < n/20 {
			t.Errorf("o dígito %d saiu %d vezes em %d — distribuição enviesada", d, count, n)
		}
	}
	// E os zeros à esquerda existem: `fmt.Sprint(n)` teria comido o primeiro.
	leading := 0
	for code := range seen {
		if strings.HasPrefix(code, "0") {
			leading++
		}
	}
	if leading == 0 {
		t.Error("nenhum código começa por zero — o preenchimento está a comê-los")
	}
}

// O código nunca é guardado: só o HMAC.
func TestHashDoesNotContainTheCode(t *testing.T) {
	p := pepper(t)
	const code = "483920"
	h, err := HashCode(code, p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(h, []byte(code)) {
		t.Fatal("o hash leva o código lá dentro")
	}
	if len(h) != 32 {
		t.Fatalf("%d bytes", len(h))
	}
}

// O pepper vive fora da base de dados: **o mesmo código com outro pepper dá
// outro hash**. É isso que torna uma fuga só da BD inútil.
func TestPepperChangesEverything(t *testing.T) {
	a, b := pepper(t), pepper(t)
	ha, _ := HashCode("483920", a)
	hb, _ := HashCode("483920", b)
	if bytes.Equal(ha, hb) {
		t.Fatal("o pepper não está a temperar nada")
	}
	if VerifyCode("483920", ha, b) {
		t.Fatal("um hash de outro pepper foi aceite")
	}
}

// Um pepper curto é recusado no arranque, não ignorado em silêncio.
func TestShortPepperIsRefused(t *testing.T) {
	if _, err := HashCode("483920", []byte("curto")); !errors.Is(err, ErrPepperTooShort) {
		t.Fatalf("esperava ErrPepperTooShort, deu %v", err)
	}
	if VerifyCode("483920", []byte("qualquer coisa"), []byte("curto")) {
		t.Fatal("verificou com um pepper inválido")
	}
}

func TestVerify(t *testing.T) {
	p := pepper(t)
	h, _ := HashCode("483920", p)

	if !VerifyCode("483920", h, p) {
		t.Fatal("o código certo devia passar")
	}
	for _, wrong := range []string{"483921", "483902", "000000", "", "48392", "4839200"} {
		if VerifyCode(wrong, h, p) {
			t.Errorf("%q foi aceite", wrong)
		}
	}
}
