package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// SessionEditStore guarda as edições ao treino de cada dia.
type SessionEditStore interface {
	Set(ctx context.Context, userID string, day time.Time, edits json.RawMessage) error
	Clear(ctx context.Context, userID string, day time.Time) error
	Since(ctx context.Context, userID string, from time.Time) ([]repo.EdicaoDeDia, error)
}

/*
 * SessionEdits serve o treino do dia como a pessoa o deixou.
 *
 * ⚠️ Tirar um exercício porque o joelho dói, trocar outro, subir as séries da
 * remada — três decisões que viviam só no telemóvel. Quem reinstalasse, ou
 * abrisse a conta noutro aparelho, encontrava o treino que o motor propõe, com
 * o agachamento de volta lá dentro.
 *
 * O que **não** está aqui: o que ficou feito. Isso é um facto do treino e mora
 * em `/v1/training/sessions`. Isto é a memória de um ecrã.
 */
type SessionEdits struct{ Store SessionEditStore }

/**
 * Um tecto para o documento.
 *
 * As edições de um dia são uma mão cheia de trocas; 32 KB dá para muito mais do
 * que isso. Sem tecto, o corpo de um pedido autenticado escreve o que quiser na
 * base de dados.
 */
const maximoBytesDeEdicoes = 32 * 1024

/** Duas semanas: o que o cliente mostra de histórico recente. */
const diasDeEdicoes = 14

/*
 * Quantos dias se olham para trás à procura de um padrão.
 *
 * Seis semanas. Menos não chega para ver três repetições de quem treina duas
 * vezes por semana; mais e uma decisão de Abril ainda estaria a pesar em Junho.
 */
const diasDeHabitos = 42

// List devolve as edições dos últimos dias.
func (h SessionEdits) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As edições do treino estão indisponíveis.", "")
		return
	}

	desde := time.Now().UTC().AddDate(0, 0, -diasDeEdicoes)
	dias, err := h.Store.Since(r.Context(), userID, desde)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as edições do treino.")
		return
	}

	out := make([]map[string]any, 0, len(dias))
	for _, d := range dias {
		out = append(out, map[string]any{
			"day":   d.Day.Format("2006-01-02"),
			"edits": d.Edits,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"days": out})
}

/*
 * Save grava as edições de um dia.
 *
 * `PUT` com o dia no caminho e o **conjunto** no corpo, como a água: o cliente
 * manda o estado do ecrã e não um acréscimo, e por isso reenviar é inofensivo.
 *
 * Um conjunto vazio apaga: "desfiz tudo" e "nunca mexi" hão-de dar o mesmo
 * treino, e dar-lhes duas representações diferentes era pedir que alguém as
 * confundisse mais tarde.
 */
func (h SessionEdits) Save(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As edições do treino estão indisponíveis.", "")
		return
	}

	dia, err := time.Parse("2006-01-02", r.PathValue("day"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "day")
		return
	}

	var req struct {
		Removed []string                  `json:"removed"`
		Added   []map[string]any          `json:"added"`
		Swapped map[string]map[string]any `json:"swapped"`
		Tuned   map[string]map[string]any `json:"tuned"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximoBytesDeEdicoes)
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	vazio := len(req.Removed) == 0 && len(req.Added) == 0 &&
		len(req.Swapped) == 0 && len(req.Tuned) == 0
	if vazio {
		if err := h.Store.Clear(r.Context(), userID, dia); err != nil {
			apierr.WriteInternal(w, r, err, "Não foi possível apagar as edições do treino.")
			return
		}
		apierr.WriteJSON(w, http.StatusOK, map[string]any{
			"day": dia.Format("2006-01-02"), "edits": nil,
		})
		return
	}

	// Volta a serializar-se o que foi lido em vez de guardar o corpo cru: o que
	// entra na base de dados passa a ser só o que estes campos descrevem, e não
	// o que alguém tenha decidido acrescentar ao pedido.
	bruto, err := json.Marshal(req)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar as edições do treino.")
		return
	}
	if err := h.Store.Set(r.Context(), userID, dia, bruto); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar as edições do treino.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"day": dia.Format("2006-01-02"), "edits": json.RawMessage(bruto),
	})
}

/*
 * Habits diz o que já deixou de ser uma troca do dia.
 *
 * ⚠️ As alterações ao treino são do dia, e está certo assim para uma dor de
 * joelho de terça-feira. Está errado para quem tira o agachamento **todas as
 * semanas** por causa de uma prótese: essa pessoa repete a mesma decisão para
 * sempre, e a app nunca aprende nada com ela.
 *
 * Isto propõe; não decide. Fixar ou excluir um exercício muda o plano inteiro,
 * e quem o faz é a pessoa — no ecrã que já existe para isso.
 */
func (h SessionEdits) Habits(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As edições do treino estão indisponíveis.", "")
		return
	}

	desde := time.Now().UTC().AddDate(0, 0, -diasDeHabitos)
	dias, err := h.Store.Since(r.Context(), userID, desde)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as edições do treino.")
		return
	}

	lidos := make([]training.EdicoesDeUmDia, 0, len(dias))
	for _, d := range dias {
		var corpo struct {
			Removed []string `json:"removed"`
			Swapped map[string]struct {
				ExerciseID string `json:"exerciseId"`
			} `json:"swapped"`
		}
		if err := json.Unmarshal(d.Edits, &corpo); err != nil {
			// Um documento que não se lê não estraga os outros: é um dia a
			// menos na contagem, e não uma resposta a menos.
			continue
		}
		trocas := map[string]string{}
		for de, para := range corpo.Swapped {
			trocas[de] = para.ExerciseID
		}
		lidos = append(lidos, training.EdicoesDeUmDia{
			Dia: d.Day.Format("2006-01-02"), Removed: corpo.Removed, Swapped: trocas,
		})
	}

	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"habits": training.HabitosEm(lidos),
		// O limiar vai na resposta para o ecrã poder dizer "três semanas
		// seguidas" sem o repetir de cabeça — e sem discordar quando mudar.
		"threshold": training.VezesParaSerHabito,
	})
}
