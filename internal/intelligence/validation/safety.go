package validation

import (
	"regexp"
	"strings"
	"unicode"
)

// MaxLabel é o comprimento de um rótulo no ecrã. "Peixe grelhado com limão"
// cabe; um parágrafo não.
const MaxLabel = 60

var linkRe = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)

/*
 * SanitizeLabel limpa o único texto livre que o modelo escreve.
 *
 * O rótulo só se mostra — não decide nada — mas vai parar a um ecrã e ao
 * diário. Por isso sai sem ligações, sem marcação, sem caracteres de controlo
 * e curto. Se não sobrar nada, o item não tem nome, e quem o chama decide.
 */
func SanitizeLabel(s string) string {
	s = linkRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		switch {
		case strings.ContainsRune("<>{}[]`*_#|\\", r):
			continue
		case unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > MaxLabel {
		out = strings.TrimSpace(string(r[:MaxLabel]))
	}
	return out
}
