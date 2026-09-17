package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/pexels"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

/*
 * Um acervo de mentira que conta quantas vezes lhe perguntam.
 *
 * É a contagem que importa: a quota é da conta inteira — 200 pedidos por hora —
 * e o defeito era cada telemóvel ir lá buscar o que outro já tinha encontrado.
 */
type acervoContado struct {
	mu      sync.Mutex
	pedidos int
	vazio   bool
}

func (a *acervoContado) SearchVideos(context.Context, string, int) ([]pexels.Video, error) {
	return nil, nil
}

func (a *acervoContado) SearchPhotos(_ context.Context, query string, _ int) ([]pexels.Photo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pedidos++
	if a.vazio {
		return nil, nil
	}
	return []pexels.Photo{{ID: 1, URI: "https://acervo/" + query + ".jpg", Alt: query}}, nil
}

func (a *acervoContado) Configured() bool { return true }

func (a *acervoContado) contagem() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pedidos
}

func serveFotos(t *testing.T, acervo handlers.MediaSource) http.Handler {
	t.Helper()
	_, pool, userID := serve(t)
	return airohttp.NewRouter(airohttp.Deps{
		Log: quietLogger(), Version: "test", DB: pool,
		Auth: fakeAuth{userID: userID},
		FoodPhotos: &handlers.FoodPhotos{
			Store: repo.NewMediaRepo(repo.NewTxManager(pool)), Source: acervo,
		},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	})
}

func fotos(t *testing.T, h http.Handler, corpo string) map[string]string {
	t.Helper()
	w := post(t, h, "/v1/media/food-photos", corpo, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d — %s", w.Code, w.Body.String())
	}
	var body struct {
		Photos map[string]string `json:"photos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta ilegível: %v — %s", err, w.Body.String())
	}
	return body.Photos
}

/*
 * Uma imagem é encontrada uma vez e serve todos.
 *
 * ⚠️ Cada telemóvel resolvia as suas: abrir a app eram dezenas de pedidos ao
 * acervo para descobrir o que toda a gente já tinha descoberto.
 */
func TestAImagemEProcuradaUmaVezSo(t *testing.T) {
	acervo := &acervoContado{}
	h := serveFotos(t, acervo)

	primeira := fotos(t, h, `{"foods":["chicken","rice"]}`)
	if len(primeira) != 2 {
		t.Fatalf("veio %+v", primeira)
	}
	if primeira["food:chicken"] == "" {
		t.Errorf("sem foto para o frango: %+v", primeira)
	}
	depoisDaPrimeira := acervo.contagem()
	if depoisDaPrimeira != 2 {
		t.Fatalf("a primeira volta fez %d buscas", depoisDaPrimeira)
	}

	// A segunda pessoa — ou o segundo ecrã — não volta ao acervo.
	segunda := fotos(t, h, `{"foods":["chicken","rice"]}`)
	if len(segunda) != 2 {
		t.Errorf("veio %+v", segunda)
	}
	if acervo.contagem() != depoisDaPrimeira {
		t.Errorf("voltou ao acervo: %d buscas", acervo.contagem())
	}
}

/*
 * Uma busca sem resultado também se guarda.
 *
 * Repetir a busca a cada ecrã é gastar a quota a confirmar ausências.
 */
func TestUmaAusenciaTambemSeGuarda(t *testing.T) {
	acervo := &acervoContado{vazio: true}
	h := serveFotos(t, acervo)

	if got := fotos(t, h, `{"foods":["chicken"]}`); len(got) != 0 {
		t.Fatalf("veio %+v", got)
	}
	primeiras := acervo.contagem()

	fotos(t, h, `{"foods":["chicken"]}`)
	if acervo.contagem() != primeiras {
		t.Errorf("voltou a procurar o que não existe: %d", acervo.contagem())
	}
}

/*
 * Um pedido não gasta a quota de uma hora.
 *
 * Sem tecto, a primeira pessoa a abrir a app numa base vazia esperava por
 * sessenta idas ao acervo — e deixava as seguintes sem nenhuma.
 */
func TestUmPedidoNaoGastaAQuotaToda(t *testing.T) {
	acervo := &acervoContado{}
	h := serveFotos(t, acervo)

	fotos(t, h, `{"foods":["chicken","rice","eggs","beans","xima","oats","milk","banana",
	                        "mango","potato","cassava","couve","carrot","okra","avocado"]}`)
	if acervo.contagem() > 8 {
		t.Fatalf("fez %d buscas num pedido só", acervo.contagem())
	}
	if acervo.contagem() == 0 {
		t.Fatal("não procurou nada")
	}
}

// Sem acervo configurado, serve o que já está guardado — e não estoira.
func TestSemAcervoServeOQueJaHa(t *testing.T) {
	h := serveFotos(t, nil)
	if got := fotos(t, h, `{"foods":["chicken"]}`); len(got) != 0 {
		t.Fatalf("veio %+v", got)
	}
}

/*
 * Um alimento sem consulta curada não se procura.
 *
 * Procurar "Xima" em vez de "ugali maize meal" devolve lixo, e uma imagem
 * errada ao lado do que a pessoa vai comer é pior do que imagem nenhuma.
 */
func TestAlimentoSemConsultaNaoSeProcura(t *testing.T) {
	acervo := &acervoContado{}
	h := serveFotos(t, acervo)

	fotos(t, h, `{"foods":["um_alimento_que_nao_existe"]}`)
	if acervo.contagem() != 0 {
		t.Errorf("foi procurar às cegas: %d buscas", acervo.contagem())
	}

	// E o texto livre procura-se pelo que a pessoa escreveu — mas não se for
	// curto de mais para dar resultado útil.
	fotos(t, h, `{"labels":["ab"]}`)
	if acervo.contagem() != 0 {
		t.Errorf("procurou uma etiqueta de duas letras: %d", acervo.contagem())
	}
	fotos(t, h, `{"labels":["pastel de nata"]}`)
	if acervo.contagem() != 1 {
		t.Errorf("etiqueta legítima: %d buscas", acervo.contagem())
	}
}
