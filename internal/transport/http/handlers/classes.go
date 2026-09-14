package handlers

import (
	"context"
	"net/http"
	"strings"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// ClassStore lê as aulas gravadas.
type ClassStore interface {
	Published(ctx context.Context, f repo.ClassFilter) ([]repo.ClassRow, error)
	Get(ctx context.Context, id string) (repo.ClassRow, error)
	// ForDay escolhe a aula do dia, ou devolve ErrNotFound — e aí o dia é o
	// plano.
	ForDay(ctx context.Context, focus, level string, equipment []string, seed int) (repo.ClassRow, error)
}

// ClassProfileReader dá o equipamento e o nível de quem pergunta.
type ClassProfileReader interface {
	EquipmentOf(ctx contextLike, userID string) ([]string, string, error)
}

// Classes serve as aulas gravadas: um especialista a dar treino.
//
// Numa aula **o vídeo lidera**. Quem decidiu os exercícios, as séries e os
// descansos foi quem a filmou — a app serve-a e grava que aconteceu.
type Classes struct {
	Store    ClassStore
	Profiles ClassProfileReader
}

// List devolve as aulas, filtradas pelo que a pessoa tem e consegue.
//
// Por omissão filtra pelo equipamento do perfil: mostrar uma aula de barra a
// quem treina em casa é mostrar uma promessa que não se cumpre. `all=1` desliga
// o filtro, para quem quer ver o que há.
func (h Classes) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As aulas estão indisponíveis.", "")
		return
	}

	q := r.URL.Query()
	f := repo.ClassFilter{
		Focus:      q.Get("focus"),
		Level:      q.Get("level"),
		Specialist: q.Get("specialist"),
	}

	if q.Get("all") != "1" && h.Profiles != nil {
		equipamento, nivel, err := h.Profiles.EquipmentOf(r.Context(), userID)
		if err == nil {
			f.Equipment = equipamento
			if f.Level == "" {
				f.Level = nivel
			}
		}
	}

	aulas, err := h.Store.Published(r.Context(), f)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as aulas.")
		return
	}

	out := make([]map[string]any, 0, len(aulas))
	for _, c := range aulas {
		out = append(out, paraAula(c))
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"classes": out, "total": len(out)})
}

// Get devolve uma aula, com o endereço do vídeo.
func (h Classes) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As aulas estão indisponíveis.", "")
		return
	}

	c, err := h.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		apierr.Write(w, apierr.NotFound, "Essa aula não existe.", "id")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, paraAula(c))
}

func paraAula(c repo.ClassRow) map[string]any {
	out := map[string]any{
		"id": c.ID, "title": c.Title, "specialist": c.Specialist,
		"focus": c.Focus, "level": c.Level,
		"durationSeconds": c.DurationSeconds, "kcal": c.Kcal,
		"videoUrl":  c.VideoURL,
		"summary":   c.Summary,
		"muscles":   nonNilStrings(c.Muscles),
		"equipment": nonNilStrings(c.Equipment),
		// A etiqueta que o cartão mostra, já escrita: "Tronco · 38 min".
		"label": etiquetaDeAula(c),
		// A duração sozinha, por extenso. O cliente não a calcula: dividia
		// segundos por sessenta à sua maneira e discordava desta etiqueta na
		// mesma aula, no mesmo ecrã.
		"durationLabel": duracaoPorExtenso(c.DurationSeconds),
		// O foco sozinho, para quem já mostra a duração ao lado e não a quer
		// dizer duas vezes em dez centímetros.
		"focusLabel": focoPorExtenso(c.Focus),
		"levelLabel": nivelPorExtenso(c.Level),
	}
	if c.ThumbnailURL != "" {
		out["thumbnailUrl"] = c.ThumbnailURL
	}
	out["equipmentLabel"] = equipamentoPorExtenso(c.Equipment)
	return out
}

// Os nomes do equipamento, como a app lhes chama.
//
// São os mesmos rótulos de `constants/plan.ts` no cliente: quem escolheu
// "Tapete" no plano tem de ler "Tapete" na aula, e não `mat`. Era o que estava
// a acontecer — o `strings.Join` das chaves mandava o slug para o ecrã, e o
// cartão da aula dizia "PRECISAS: mat".
var equipamentosPorExtenso = map[string]string{
	"bodyweight": "Só o corpo",
	"dumbbells":  "Halteres",
	"bands":      "Elásticos",
	"kettlebell": "Kettlebell",
	"barbell":    "Barra e discos",
	"machines":   "Máquinas",
	"cardio":     "Passadeira ou bicicleta",
	"mat":        "Tapete",
}

func equipamentoPorExtenso(equipamento []string) string {
	// Vazio quer dizer "só o corpo", e o ecrã tem de o dizer assim em vez de
	// mostrar uma lista em branco.
	if len(equipamento) == 0 {
		return "Só o corpo"
	}
	nomes := make([]string, 0, len(equipamento))
	for _, e := range equipamento {
		if v, ok := equipamentosPorExtenso[e]; ok {
			nomes = append(nomes, v)
			continue
		}
		// Um equipamento que o servidor não conhece vai como está: melhor um
		// slug no ecrã do que um campo vazio.
		nomes = append(nomes, e)
	}
	return strings.Join(nomes, ", ")
}

var focosPorExtenso = map[string]string{
	"upper": "Tronco", "lower": "Pernas", "cardio": "Cardio",
	"full": "Corpo inteiro", "mobility": "Mobilidade",
}

var niveisPorExtenso = map[string]string{
	"beginner": "Iniciante", "intermediate": "Intermédio", "advanced": "Avançado",
}

func etiquetaDeAula(c repo.ClassRow) string {
	return focoPorExtenso(c.Focus) + " · " + duracaoPorExtenso(c.DurationSeconds)
}

/*
 * A duração por extenso.
 *
 * Duas correcções a uma divisão inteira, e ambas apareceram no ecrã:
 *
 *   - **Arredonda**, não trunca. Uma aula de 1 min 59 s dizia "1 min" aqui e
 *     "2 min" no ecrã da aula, que arredondava — o mesmo número, dois valores,
 *     a dez centímetros um do outro.
 *   - **Abaixo do minuto diz segundos.** Truncar dava "Tronco · 0 min", que é
 *     uma aula a anunciar que não dura nada.
 */
func duracaoPorExtenso(segundos int) string {
	if segundos < 60 {
		if segundos < 1 {
			segundos = 1
		}
		return plural(segundos) + " s"
	}
	return plural((segundos+30)/60) + " min"
}

func focoPorExtenso(f string) string {
	if v, ok := focosPorExtenso[f]; ok {
		return v
	}
	return f
}

func nivelPorExtenso(l string) string {
	if v, ok := niveisPorExtenso[l]; ok {
		return v
	}
	return l
}

func plural(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
