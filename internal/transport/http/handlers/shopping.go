package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
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
 * ⚠️ **A lista não se guarda, e é de propósito.** Guardá-la seria uma segunda
 * cópia a envelhecer sozinha, e quem mudasse de estratégia a meio da semana
 * ficava com uma lista de comida que já não ia comer. O que fica gravado é só
 * o que não se consegue recalcular: o que foi riscado ("isto já tenho em
 * casa"), o que foi acrescentado à mão, e o preço de cada linha.
 *
 * ⚠️ **Mas quem diz o que entra na lista é o plano, e não quem a desenha.**
 * O telemóvel montava-a sozinho, chamando o motor de nutrição outra vez para
 * os dias que faltavam na semana. Duas consequências, e a segunda dava para
 * ver no ecrã:
 *
 *  1. A mesma conta em dois telemóveis com versões diferentes da app ia ao
 *     mercado com duas listas. A lista era função da app instalada, e não da
 *     conta.
 *  2. **As trocas de refeição eram ignoradas.** Quem trocasse o almoço de
 *     quinta continuava a levar para o mercado os ingredientes do almoço que
 *     tinha trocado — porque `BuildDayPlan` sozinho não sabe das trocas, e
 *     quem sabe delas é o serviço.
 *
 * Por isso a lista sai daqui já decidida, do mesmo `Plans.Today` que serve o
 * ecrã da nutrição — trocas incluídas. É a mesma regra que `handlers/nutrition.go`
 * já escrevia para o dia: nenhuma destas decisões volta a ser tomada no telemóvel.
 */
type Shopping struct {
	Store ShoppingStore
	/*
	 * O plano, dia a dia. O mesmo que serve `GET /v1/nutrition/today`.
	 *
	 * Ausente, a lista devolve só o que a pessoa escreveu — que é a resposta
	 * certa para quem ainda não tem perfil, e não um erro.
	 */
	Plans    NutritionPlanner
	Profiles NutritionProfileReader
	Clock    clock.Clock
}

// NutritionPlanner monta o dia já decidido, com as trocas da pessoa por cima.
type NutritionPlanner interface {
	Today(ctx context.Context, in service.NutritionTodayInput) (service.NutritionToday, error)
}

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
			"items":   h.doPlano(r.Context(), userID, semana),
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
		"items":   h.doPlano(r.Context(), userID, semana),
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
		// Também na escrita: a resposta é a lista inteira, e o cliente aplica-a
		// tal como veio. Devolver metade obrigava-o a juntar as duas partes —
		// que é exactamente o trabalho que ele deixou de fazer.
		"items": h.doPlano(r.Context(), userID, semana),
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

/*
 * Uma linha que o plano põe na lista.
 *
 * Não leva preço: o preço é da pessoa e vive no estado, não no plano. Aqui só
 * está o que se compra e quanto.
 */
type itemDoPlano struct {
	FoodID   string `json:"foodId"`
	Name     string `json:"name"`
	Category string `json:"category"`
	/** Para a semana inteira, já arredondado para cima. */
	Grams int `json:"grams"`
}

/*
 * doPlano soma os ingredientes dos dias que **faltam** desta semana.
 *
 * Só os que faltam: a compra de quarta não inclui a segunda que já passou, e
 * uma lista que peça comida de dias comidos é uma lista que se deita fora.
 *
 * Um dia que o motor não consegue fechar não estraga a semana: o que interessa
 * é a soma, e falta-lhe um dia em vez de faltar tudo. Sem perfil não há plano
 * nenhum — e a lista volta com o que a pessoa escreveu à mão, que continua a
 * ser a resposta certa e não um erro.
 *
 * São sete chamadas ao serviço e não uma: o dia muda com o treino, e o treino
 * muda com o dia da semana. Este ecrã abre-se uma ou duas vezes por semana —
 * escrever um caminho próprio para poupar seis leituras de perfil era trocar
 * clareza por tempo que ninguém está a contar.
 */
func (h Shopping) doPlano(ctx context.Context, userID string, semana time.Time) []itemDoPlano {
	if h.Plans == nil || h.Profiles == nil {
		return []itemDoPlano{}
	}

	hoje := time.Now().UTC()
	if h.Clock != nil {
		hoje = h.Clock.Now().UTC()
	}
	hoje = time.Date(hoje.Year(), hoje.Month(), hoje.Day(), 0, 0, 0, 0, time.UTC)

	inicio := semana
	if hoje.After(inicio) {
		inicio = hoje
	}
	fim := semana.AddDate(0, 0, 6)
	if inicio.After(fim) {
		// Uma semana que já passou não tem dias por comprar.
		return []itemDoPlano{}
	}

	gramas := map[string]float64{}
	for dia := inicio; !dia.After(fim); dia = dia.AddDate(0, 0, 1) {
		in, err := h.Profiles.NutritionProfile(ctx, userID, dia)
		if err != nil {
			return []itemDoPlano{}
		}
		out, err := h.Plans.Today(ctx, in)
		if err != nil {
			continue
		}
		for _, refeicao := range out.Day.Meals {
			for _, item := range refeicao.Items {
				gramas[item.FoodID] += item.Grams
			}
		}
	}

	itens := make([]itemDoPlano, 0, len(gramas))
	for id, g := range gramas {
		alimento, ok := nutrition.GetFood(id)
		// Um alimento que o catálogo já não tem não vai para a lista: ninguém
		// o compra e ocupava uma linha a dizer o seu próprio identificador.
		if !ok || g <= 0 {
			continue
		}
		itens = append(itens, itemDoPlano{
			FoodID: id, Name: alimento.Name, Category: string(alimento.Category),
			Grams: arredondaAoMeioHecto(g),
		})
	}

	/*
	 * Ordenado como se anda no mercado, e não por alfabeto.
	 *
	 * A ordem vem daqui e não do telemóvel: é uma decisão sobre a lista, e a
	 * lista é do plano. Dentro de cada grupo, do que pesa mais para o que pesa
	 * menos — é o que enche o saco primeiro.
	 */
	sort.SliceStable(itens, func(i, j int) bool {
		a, b := ordemDoGrupo(itens[i].Category), ordemDoGrupo(itens[j].Category)
		if a != b {
			return a < b
		}
		if itens[i].Grams != itens[j].Grams {
			return itens[i].Grams > itens[j].Grams
		}
		return itens[i].Name < itens[j].Name
	})
	return itens
}

/*
 * Arredonda para cima, ao meio hecto.
 *
 * Ninguém compra 437 g de frango. Para cima porque faltar comida a meio da
 * semana é pior do que sobrar.
 */
func arredondaAoMeioHecto(g float64) int {
	return int(math.Ceil(g/50) * 50)
}

/** A ordem por que se anda no mercado. O "outros" vai ao fim, com o sabão. */
func ordemDoGrupo(c string) int {
	for i, g := range []string{"protein", "vegetable", "carb", "fruit", "dairy", "fat"} {
		if g == c {
			return i
		}
	}
	return 99
}
