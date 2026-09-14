package handlers

import (
	"fmt"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

/*
 * A vista de uma playlist.
 *
 * Sai daqui **tudo decidido**: quantas faltam, qual é a próxima, se acabou, e
 * o que cada linha diz. O cliente desenha sem um único `if` de domínio — é a
 * mesma regra do pacote da sessão (T4.8), e existe pela mesma razão: duas
 * contas do mesmo número, uma de cada lado, mais cedo ou mais tarde discordam.
 */
func paraPlaylist(
	p repo.PlaylistRow,
	itens []repo.PlaylistItemRow,
	estados map[int]repo.PlaylistStateRow,
	objetivoDaPessoa string,
	comItens bool,
) map[string]any {
	feitos, saltados, segundos := 0, 0, 0
	// A próxima é a primeira sem marca nenhuma. Uma saltada não é a próxima:
	// a pessoa já disse que hoje não.
	proxima := 0
	linhas := make([]map[string]any, 0, len(itens))

	for _, it := range itens {
		segundos += it.Class.DurationSeconds
		estado := estados[it.Position]
		switch estado.Status {
		case "done":
			feitos++
		case "skipped":
			saltados++
		default:
			if proxima == 0 {
				proxima = it.Position
			}
		}
		if comItens {
			linha := paraAula(it.Class)
			linha["position"] = it.Position
			linha["status"] = estadoOuPorFazer(estado.Status)
			linha["statusLabel"] = estadoPorExtenso(estado.Status)
			linha["watchedSeconds"] = estado.WatchedSeconds
			linhas = append(linhas, linha)
		}
	}

	total := len(itens)
	// Terminada quando não sobra nenhuma por fazer — com saltadas lá dentro,
	// que é o que "saltar é hoje não" quer dizer: não impede acabar.
	terminada := total > 0 && proxima == 0
	// A percentagem é só do que foi feito. Uma lista acabada com metade
	// saltada não é uma lista feita a 100%, e dizê-lo seria mentir a quem a fez.
	fraccao := 0.0
	if total > 0 {
		fraccao = float64(feitos) / float64(total)
	}

	if comItens && proxima > 0 {
		for _, linha := range linhas {
			if linha["position"] == proxima {
				linha["isNext"] = true
			}
		}
	}

	out := map[string]any{
		"id": p.ID, "title": p.Title, "summary": p.Summary,
		"goals": nonNilStrings(p.Goals), "zones": nonNilStrings(p.Zones),
		"level":      p.Level,
		"levelLabel": nivelPorExtenso(p.Level),

		"classCount":      total,
		"durationSeconds": segundos,
		"durationLabel":   duracaoPorExtenso(segundos),
		// A etiqueta do cartão, já escrita: "4 aulas · 5 min".
		"label": fmt.Sprintf("%s · %s", aulasPorExtenso(total), duracaoPorExtenso(segundos)),

		"progress": map[string]any{
			"done": feitos, "skipped": saltados, "total": total,
			"remaining": total - feitos - saltados,
			"percent":   fraccao,
			"label":     progressoPorExtenso(feitos, total),
			"completed": terminada,
			// Uma lista terminada com saltadas diz o que ficou por fazer, em
			// vez de se dar por completa em silêncio.
			"note": notaDeFim(terminada, saltados),
		},
		"nextPosition": proxima,
		"forYou":       serveObjetivo(p.Goals, objetivoDaPessoa),
	}
	if p.CoverURL != "" {
		out["coverUrl"] = p.CoverURL
	}
	if comItens {
		out["items"] = linhas
	}
	return out
}

// "por fazer" não se guarda: é a ausência de marca. Mas o ecrã precisa de um
// nome para ela, e inventá-lo no cliente seria pôr lá uma regra.
func estadoOuPorFazer(s string) string {
	if s == "" {
		return "pending"
	}
	return s
}

func estadoPorExtenso(s string) string {
	switch s {
	case "done":
		return "Feito"
	case "skipped":
		return "Saltado"
	default:
		return "Por fazer"
	}
}

func aulasPorExtenso(n int) string {
	if n == 1 {
		return "1 aula"
	}
	return fmt.Sprintf("%d aulas", n)
}

func progressoPorExtenso(feitos, total int) string {
	if total == 0 {
		return "Sem aulas"
	}
	if feitos == 0 {
		return fmt.Sprintf("Por começar · %s", aulasPorExtenso(total))
	}
	return fmt.Sprintf("%d de %d feitos", feitos, total)
}

func notaDeFim(terminada bool, saltados int) string {
	if !terminada {
		return ""
	}
	switch {
	case saltados == 0:
		return "Fizeste a lista toda."
	case saltados == 1:
		return "Chegaste ao fim, com uma aula saltada."
	default:
		return fmt.Sprintf("Chegaste ao fim, com %d aulas saltadas.", saltados)
	}
}

func serveObjetivo(objetivos []string, meu string) bool {
	if meu == "" {
		return false
	}
	for _, g := range objetivos {
		if g == meu {
			return true
		}
	}
	return false
}
