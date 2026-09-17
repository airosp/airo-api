package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// MediaStore guarda as imagens já encontradas.
type MediaStore interface {
	Buscar(ctx context.Context, assuntos []string) (map[string]repo.Imagem, error)
	Guardar(ctx context.Context, i repo.Imagem) error
}

/*
 * FoodPhotos devolve as fotografias dos alimentos, resolvidas uma vez para
 * todos.
 *
 * ⚠️ Cada telemóvel resolvia as suas: abrir a app eram dezenas de pedidos ao
 * acervo para descobrir o que toda a gente já tinha descoberto. E a quota é da
 * **conta inteira** — 200 pedidos por hora —, não de cada pessoa: com alguma
 * gente a usar a app ao mesmo tempo, as fotografias deixavam de aparecer, sem
 * nada no ecrã a explicar porquê.
 *
 * Aqui uma imagem é encontrada uma vez e serve todos. E o que se procura deixou
 * de viver no telemóvel: escolher a consulta certa para "Xima" é uma decisão,
 * não uma preferência de quem tem a app instalada.
 */
type FoodPhotos struct {
	Store  MediaStore
	Source MediaSource
}

const (
	/** Quantos alimentos se resolvem de uma vez. Uma lista de refeições tem dezenas. */
	maximoAlimentosPorPedido = 60
	/*
	 * Quantas buscas novas se fazem num pedido.
	 *
	 * O resto vem na próxima volta. Sem tecto, a primeira pessoa a abrir a app
	 * numa base vazia esperava por sessenta idas ao acervo — e gastava a quota
	 * de uma hora numa só.
	 */
	maximoBuscasPorPedido = 8
	/*
	 * Quanto tempo se acredita numa busca que não encontrou nada.
	 *
	 * Uma semana. O acervo cresce, e um alimento sem foto hoje pode ter uma no
	 * mês que vem — mas voltar a procurar a cada ecrã é gastar a quota a
	 * confirmar ausências.
	 */
	ttlDeFalha = 7 * 24 * time.Hour
)

/*
 * List resolve um lote de alimentos.
 *
 * `POST` e não `GET` porque a lista é o pedido: sessenta identificadores numa
 * query string é um endereço que os intermediários cortam.
 */
func (h FoodPhotos) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserID(r.Context()); !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As imagens estão indisponíveis.", "")
		return
	}

	var req struct {
		// Foods são identificadores do catálogo: "chicken", "xima".
		Foods []string `json:"foods"`
		// Labels é o que a pessoa escreveu sobre o que comeu.
		Labels []string `json:"labels"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	// Assunto → o que procurar. O prefixo diz o tipo e mantém a chave única.
	consultas := map[string]string{}
	for _, id := range req.Foods {
		id = strings.TrimSpace(id)
		if id == "" || len(consultas) >= maximoAlimentosPorPedido {
			continue
		}
		if q := nutrition.ConsultaDoAlimento(id); q != "" {
			consultas["food:"+id] = q
		}
	}
	for _, etiqueta := range req.Labels {
		if len(consultas) >= maximoAlimentosPorPedido {
			break
		}
		if q := nutrition.ConsultaDeEtiqueta(etiqueta); q != "" {
			consultas["label:"+q] = q
		}
	}

	assuntos := make([]string, 0, len(consultas))
	for k := range consultas {
		assuntos = append(assuntos, k)
	}
	conhecidas, err := h.Store.Buscar(r.Context(), assuntos)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as imagens.")
		return
	}

	// O que falta procurar, com tecto. Uma busca falhada há pouco não se repete.
	buscas := 0
	if h.Source != nil && h.Source.Configured() {
		for assunto, query := range consultas {
			if buscas >= maximoBuscasPorPedido {
				break
			}
			if i, existe := conhecidas[assunto]; existe {
				if i.URL != "" || time.Since(i.FetchedAt) < ttlDeFalha {
					continue
				}
			}
			buscas++

			fotos, err := h.Source.SearchPhotos(r.Context(), query, 1)
			if err != nil {
				// O acervo em baixo não estraga o que já se sabe: devolve-se o
				// que há, e a próxima volta tenta o resto.
				continue
			}
			imagem := repo.Imagem{Subject: assunto}
			if len(fotos) > 0 {
				imagem.URL = fotos[0].URI
				imagem.Credit = fotos[0].Alt
			}
			// Guarda-se mesmo quando não há nada: uma ausência também é uma
			// resposta, e repetir a busca a cada ecrã é gastar quota a
			// confirmar ausências.
			if err := h.Store.Guardar(r.Context(), imagem); err == nil {
				conhecidas[assunto] = imagem
			}
		}
	}

	fotos := map[string]string{}
	for assunto, i := range conhecidas {
		if i.URL != "" {
			fotos[assunto] = i.URL
		}
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"photos": fotos,
		// Quantas ficaram por resolver nesta volta: o cliente sabe que vale a
		// pena voltar a perguntar, em vez de assumir que não há imagem.
		"pending": len(consultas) - len(conhecidas),
	})
}
