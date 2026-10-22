package policy

import (
	"regexp"
	"strings"
	"unicode"
)

// MaxMealText é o comprimento máximo de um registo por texto. "Comi xima com
// matapa e peixe grelhado" tem trinta e oito caracteres; quinhentos chegam
// para um dia inteiro e não chegam para colar um documento.
const MaxMealText = 500

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	urlRe   = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)
	// Sequências de oito ou mais dígitos, com ou sem espaços e sinal: números
	// de telefone e documentos. "250 g" e "2 ovos" ficam como estão.
	phoneRe = regexp.MustCompile(`\+?\d(?:[\s\-.]?\d){7,}`)
)

/*
 * RedactText tira do texto o que não é preciso para saber o que se comeu.
 *
 * O fornecedor recebe a frase e mais nada: nem o nome, nem o telefone, nem o
 * identificador de quem a escreveu. E se a pessoa escrever um email ou um
 * número de telefone no meio do almoço — acontece, quando se cola de outro
 * sítio —, sai também.
 *
 * Também normaliza: caracteres de controlo fora, espaços colapsados, e o
 * comprimento cortado. Um texto com quebras de linha e marcadores é um texto à
 * procura de parecer instruções.
 */
func RedactText(s string) string {
	s = emailRe.ReplaceAllString(s, "[email]")
	s = urlRe.ReplaceAllString(s, "[endereço]")
	s = phoneRe.ReplaceAllString(s, "[número]")

	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > MaxMealText {
		out = string(r[:MaxMealText])
	}
	return out
}
