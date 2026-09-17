package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// ShoppingStore guarda o que a pessoa decidiu sobre a lista de compras.
type ShoppingStore interface {
	Set(ctx context.Context, userID string, semana time.Time, estado json.RawMessage) error
	Get(ctx context.Context, userID string, semana time.Time) (json.RawMessage, error)
	Prune(ctx context.Context, userID string, antesDe time.Time) error
}

/*
 * Shopping serve a lista de compras da semana.
 *
 * ⚠️ **A lista não está aqui, e é de propósito.** Ela sai do plano alimentar,
 * que o motor já sabe fazer; guardá-la seria uma segunda cópia a envelhecer
 * sozinha, e quem mudasse de estratégia a meio da semana ficava com uma lista
 * de comida que já não ia comer.
 *
 * O que fica é o que não se consegue recalcular: o que foi riscado ("isto já
 * tenho em casa") e o que foi acrescentado à mão — o sabão, o pão, a coisa que
 * o motor não sabe.
 */
type Shopping struct{ Store ShoppingStore }

const (
	/** Uma lista de compras não tem mil linhas. */
	maximoItensRiscados = 300
	maximoExtras        = 100
	maximoLetrasDoItem  = 80
	/** Quanto tempo se guardam as semanas passadas antes de irem fora. */
	semanasDeListas = 2
)

type estadoDaLista struct {
	Checked []string `json:"checked"`
	Extras  []extra  `json:"extras"`
	/*
	 * Quanto custa cada linha, em meticais, pela chave da linha.
	 *
	 * ⚠️ Uma lista de compras sem preços é uma lista de intenções: ninguém sai
	 * de casa sem saber se o que está no papel cabe no que tem na carteira. E
	 * o preço não vem do plano — varia de mercado para mercado e de semana
	 * para semana —, por isso é a pessoa que o aponta, e é dela.
	 *
	 * A chave é a da linha: `chicken` para um alimento do plano, o id do extra
	 * para o que foi escrito à mão. Assim o preço sobrevive a mudar de semana
	 * enquanto o alimento continuar na lista.
	 */
	Precos map[string]float64 `json:"prices,omitempty"`
}

// Um item escrito à mão. O `id` é do cliente — é ele que tem a lista aberta.
type extra struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	/*
	 * Em que grupo entra na lista.
	 *
	 * ⚠️ Tudo o que era escrito à mão caía num saco no fim chamado "mais
	 * alguma coisa". Quem quisesse acrescentar manga ia à fruta e não a
	 * encontrava lá — encontrava-a no fim, longe do resto da fruta, que é
	 * exactamente onde não serve a quem anda pelo mercado por secções.
	 *
	 * Vazio cai em "outros", que é para o sabão e para o que não é comida.
	 */
	Category string `json:"category,omitempty"`
	/** Quanto comprar. Zero quer dizer que a pessoa não disse. */
	Grams int `json:"grams,omitempty"`
}

// Get devolve o estado de uma semana. Vazio quando ainda não foi tocada.
func (h Shopping) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "A lista de compras está indisponível.", "")
		return
	}

	semana, err := semanaDe(r.PathValue("week"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, err.Error(), "week")
		return
	}

	bruto, err := h.Store.Get(r.Context(), userID, semana)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		// Uma semana por tocar não é um erro: é uma lista por riscar.
		apierr.WriteJSON(w, http.StatusOK, map[string]any{
			"week":    semana.Format("2006-01-02"),
			"checked": []string{},
			"extras":  []extra{},
			"prices":  map[string]float64{},
		})
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler a lista de compras.")
		return
	}

	var estado estadoDaLista
	if err := json.Unmarshal(bruto, &estado); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler a lista de compras.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"week":    semana.Format("2006-01-02"),
		"checked": naoNil(estado.Checked),
		"extras":  extrasNaoNil(estado.Extras),
		"prices":  naoNilPrecos(estado.Precos),
	})
}

/*
 * Save grava o estado de uma semana.
 *
 * O cliente manda o estado inteiro, como faz com a água e com o treino do dia:
 * reenviar dá o mesmo resultado, que é o que salva uma rede que repete pedidos.
 */
func (h Shopping) Save(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "A lista de compras está indisponível.", "")
		return
	}

	semana, err := semanaDe(r.PathValue("week"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, err.Error(), "week")
		return
	}

	var req estadoDaLista
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if len(req.Precos) > maximoItensRiscados {
		apierr.Write(w, apierr.ValidationFailed, "Demasiados preços.", "prices")
		return
	}
	if len(req.Checked) > maximoItensRiscados {
		apierr.Write(w, apierr.ValidationFailed, "Riscaste mais do que cabe numa lista.", "checked")
		return
	}
	if len(req.Extras) > maximoExtras {
		apierr.Write(w, apierr.ValidationFailed, "Demasiados itens acrescentados.", "extras")
		return
	}

	limpo := estadoDaLista{Checked: []string{}, Extras: []extra{}}
	vistos := map[string]bool{}
	for _, id := range req.Checked {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maximoLetrasDoItem || vistos[id] {
			continue
		}
		vistos[id] = true
		limpo.Checked = append(limpo.Checked, id)
	}
	for _, e := range req.Extras {
		e.ID, e.Label, e.Note = strings.TrimSpace(e.ID), strings.TrimSpace(e.Label), strings.TrimSpace(e.Note)
		// Um item sem nome não é um item. Sem isto, tocar em "acrescentar" sem
		// escrever nada deixava uma linha em branco na lista para sempre.
		if e.ID == "" || e.Label == "" || len(e.ID) > maximoLetrasDoItem {
			continue
		}
		// Um grupo que a app não conhece cai em "outros": é melhor a manga
		// aparecer no fim do que desaparecer.
		categoria := strings.TrimSpace(e.Category)
		if !gruposDaLista[categoria] {
			categoria = "outros"
		}
		limpo.Extras = append(limpo.Extras, extra{
			ID: e.ID, Label: corta(e.Label, maximoLetrasDoItem), Note: corta(e.Note, maximoLetrasDoItem),
			Category: categoria,
			Grams:    limitar(e.Grams, 0, maximoGramas),
		})
	}

	/*
	 * Os preços, limpos.
	 *
	 * Negativos e absurdos ficam de fora: um preço é o que se paga, e um saco
	 * de arroz não custa um milhão de meticais. Zero também sai — quer dizer
	 * "não apontei", e guardá-lo era guardar a ausência de uma resposta.
	 */
	if len(req.Precos) > 0 {
		limpo.Precos = map[string]float64{}
		for chave, valor := range req.Precos {
			chave = strings.TrimSpace(chave)
			if chave == "" || len(chave) > maximoLetrasDoItem {
				continue
			}
			if valor <= 0 || valor > maximoPreco || math.IsNaN(valor) || math.IsInf(valor, 0) {
				continue
			}
			// Duas casas: o metical tem centavos e mais do que isso é ruído.
			limpo.Precos[chave] = math.Round(valor*100) / 100
		}
	}

	bruto, err := json.Marshal(limpo)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a lista de compras.")
		return
	}
	if err := h.Store.Set(r.Context(), userID, semana, bruto); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a lista de compras.")
		return
	}
	// As semanas passadas vão fora à boleia desta escrita: não vale um trabalho
	// agendado só para deitar fora uma lista de couves de há um mês.
	// Falhar a limpar não é falhar a gravar: o que a pessoa acabou de riscar já
	// está guardado, e é isso que ela veio fazer. Na próxima escrita tenta-se
	// outra vez.
	_ = h.Store.Prune(r.Context(), userID, semana.AddDate(0, 0, -7*semanasDeListas))

	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"week":    semana.Format("2006-01-02"),
		"checked": limpo.Checked,
		"extras":  limpo.Extras,
		"prices":  naoNilPrecos(limpo.Precos),
	})
}

/*
 * Os grupos em que uma linha pode entrar.
 *
 * São os do catálogo de alimentos mais "outros", que é para o sabão e para o
 * que não é comida. Estão aqui e não numa tabela porque são a forma da lista
 * — mudá-los é mudar como se anda pelo mercado, não é dado.
 */
var gruposDaLista = map[string]bool{
	"protein": true, "carb": true, "vegetable": true,
	"fat": true, "fruit": true, "dairy": true, "outros": true,
}

const (
	/** Cem quilos: acima disto é erro de dedo, não uma compra. */
	maximoGramas = 100000
	/** Um milhão de meticais numa linha é erro de dedo, não um preço. */
	maximoPreco = 1000000.0
)

func limitar(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func naoNilPrecos(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}

/*
 * semanaDe lê o dia e devolve a segunda-feira dessa semana.
 *
 * Normaliza-se aqui em vez de confiar no cliente: dois aparelhos com ideias
 * diferentes sobre onde começa a semana davam duas listas para a mesma semana,
 * e a pessoa via metade do que tinha riscado.
 */
func semanaDe(valor string) (time.Time, error) {
	d, err := time.Parse("2006-01-02", valor)
	if err != nil {
		return time.Time{}, errors.New("Semana inválida.")
	}
	// Em Go, o domingo é 0; aqui a semana começa à segunda, como no calendário
	// da app e como em Moçambique.
	recuo := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -recuo), nil
}

func corta(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max])
}

func naoNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func extrasNaoNil(e []extra) []extra {
	if e == nil {
		return []extra{}
	}
	return e
}
