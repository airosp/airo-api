package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

// CodeLength — seis dígitos.
//
// Convenção, e o preenchimento automático do WhatsApp espera-o. O alfabeto é
// `0-9` porque o teclado numérico reduz erros a transcrever.
const CodeLength = 6

// MinPepperBytes — abaixo disto o pepper não acrescenta nada.
const MinPepperBytes = 32

var ErrPepperTooShort = errors.New("pepper curto demais")

// GenerateCode devolve um código de seis dígitos.
//
// ⚠️ `crypto/rand`, **nunca** `math/rand`. Um gerador previsível é o mesmo que
// não ter código nenhum: quem souber a semente sabe o código de toda a gente.
//
// A distribuição é uniforme por construção — `rand.Int` sobre 10^6 não tem o
// enviesamento que `n % 10^6` teria.
func GenerateCode() (string, error) {
	max := big.NewInt(1)
	for i := 0; i < CodeLength; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("gerar código: %w", err)
	}
	return fmt.Sprintf("%0*d", CodeLength, n), nil
}

// HashCode devolve `HMAC-SHA256(código, pepper)`.
//
// **O código nunca é guardado.** Três razões para HMAC com pepper em vez de
// hash simples ou bcrypt:
//
//   - Hash simples não chega: 10⁶ possibilidades geram-se numa tabela completa
//     em segundos, e uma fuga da base de dados entregaria todos os códigos
//     activos.
//   - O pepper vive **fora** da base de dados, num gestor de segredos. Uma fuga
//     só da BD deixa os códigos inúteis.
//   - bcrypt seria mais seguro se o pepper também vazasse, mas acrescenta ~100 ms
//     a cada verificação — e o código expira em cinco minutos de qualquer forma.
//
// É um compromisso, não uma verdade absoluta. Com requisito de conformidade que
// exija derivação lenta, troca-se por Argon2id e aceita-se a latência.
func HashCode(code string, pepper []byte) ([]byte, error) {
	if len(pepper) < MinPepperBytes {
		return nil, fmt.Errorf("%w: %d bytes, mínimo %d", ErrPepperTooShort, len(pepper), MinPepperBytes)
	}
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(code))
	return mac.Sum(nil), nil
}

// VerifyCode compara em **tempo constante**.
//
// `hmac.Equal` e não `bytes.Equal`: a comparação byte a byte pára no primeiro
// que difere, e a diferença de tempo é um canal lateral que permite descobrir o
// código dígito a dígito.
func VerifyCode(given string, stored, pepper []byte) bool {
	computed, err := HashCode(given, pepper)
	if err != nil {
		return false
	}
	return hmac.Equal(computed, stored)
}
