package cloudinary_test

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/platform/cloudinary"
)

// campos lê o formulário que o cliente enviou.
func campos(t *testing.T, r *http.Request) (map[string]string, []byte) {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	out := map[string]string{}
	var file []byte
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(part)
		if part.FormName() == "file" {
			file = body
			continue
		}
		out[part.FormName()] = string(body)
	}
	return out, file
}

func cliente(t *testing.T, h http.HandlerFunc) *cloudinary.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := cloudinary.New(cloudinary.Config{
		CloudName: "savanapoint", APIKey: "chave", APISecret: "segredo",
		BaseURL: srv.URL, DeliveryBase: "https://res.cloudinary.test",
		Now: func() time.Time { return time.Unix(1789000000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEnvioAssinaEPedeCaras(t *testing.T) {
	var form map[string]string
	var file []byte

	c := cliente(t, func(w http.ResponseWriter, r *http.Request) {
		form, file = campos(t, r)
		_, _ = w.Write([]byte(`{"public_id":"airo/profiles/u1","version":17,"format":"jpg",
		                        "width":900,"height":1200,"faces":[[380,210,140,140]]}`))
	})

	up, err := c.Upload(context.Background(), []byte("bytes-da-foto"), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if string(file) != "bytes-da-foto" {
		t.Fatalf("ficheiro = %q", file)
	}
	if form["public_id"] != "u1" || form["folder"] != "airo/profiles" {
		t.Fatalf("destino = %v/%v", form["folder"], form["public_id"])
	}
	// Substituir em vez de acumular: sem isto, cada troca de fotografia deixava
	// uma cópia órfã na conta para sempre.
	if form["overwrite"] != "true" || form["invalidate"] != "true" {
		t.Fatalf("overwrite=%v invalidate=%v", form["overwrite"], form["invalidate"])
	}
	// Sem `faces=true` não há como saber se há cara — e o enquadramento passava
	// a ser um palpite.
	if form["faces"] != "true" {
		t.Fatal("o envio tem de pedir a deteção de caras")
	}

	// A assinatura é de SHA-1 sobre os parâmetros ordenados, **sem** api_key.
	// Incluí-la dá "Invalid Signature" sem dizer porquê.
	// Por ordem alfabética da chave, e o segredo colado no fim.
	esperada := sha1Hex("faces=true&folder=airo/profiles&invalidate=true&overwrite=true&public_id=u1&timestamp=1789000000segredo")
	if form["signature"] != esperada {
		t.Fatalf("assinatura = %s, esperava %s", form["signature"], esperada)
	}
	if form["api_key"] != "chave" {
		t.Fatalf("api_key = %q", form["api_key"])
	}
	if !up.FaceFound() {
		t.Fatal("a resposta trazia uma cara")
	}
}

// Com cara, o corte segue-a. Sem cara, não se finge que segue.
func TestEnquadramentoSegueACara(t *testing.T) {
	comCara := cloudinary.Uploaded{PublicID: "airo/profiles/u1", Version: 17, Format: "jpg",
		Faces: [][]int{{380, 210, 140, 140}}}
	semCara := cloudinary.Uploaded{PublicID: "airo/profiles/u2", Version: 3, Format: "png"}

	c := cliente(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })

	full, thumb := c.AvatarURLs(comCara)
	if !strings.Contains(full, "g_face") {
		t.Fatalf("com cara, o corte tem de a seguir: %s", full)
	}
	if !strings.Contains(full, "w_512,h_512") || !strings.Contains(thumb, "w_128,h_128") {
		t.Fatalf("tamanhos = %s / %s", full, thumb)
	}
	if !strings.HasPrefix(full, "https://res.cloudinary.test/savanapoint/image/upload/") {
		t.Fatalf("endereço = %s", full)
	}
	// A versão entra no endereço: é o que faz a fotografia nova aparecer em vez
	// da que a CDN tem guardada.
	if !strings.Contains(full, "/v17/airo/profiles/u1.jpg") {
		t.Fatalf("faltam a versão ou o ficheiro: %s", full)
	}

	sem, _ := c.AvatarURLs(semCara)
	if strings.Contains(sem, "g_face") {
		t.Fatalf("sem cara não se finge que há: %s", sem)
	}
	if !strings.Contains(sem, "g_auto") {
		t.Fatalf("sem cara, centra-se no que a imagem tem: %s", sem)
	}
}

func TestErroDaCloudinaryChega(t *testing.T) {
	c := cliente(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid image file"}}`))
	})
	_, err := c.Upload(context.Background(), []byte("x"), "u1")
	if err == nil || !strings.Contains(err.Error(), "Invalid image file") {
		t.Fatalf("erro = %v", err)
	}
}

func TestConfiguracaoIncompletaRecusa(t *testing.T) {
	casos := []struct {
		nome string
		cfg  cloudinary.Config
	}{
		{"sem conta", cloudinary.Config{APIKey: "k", APISecret: "s"}},
		{"sem chave", cloudinary.Config{CloudName: "c", APISecret: "s"}},
		{"sem segredo", cloudinary.Config{CloudName: "c", APIKey: "k"}},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if _, err := cloudinary.New(caso.cfg); err == nil {
				t.Fatal("devia recusar")
			}
		})
	}
}

func sha1Hex(s string) string {
	h := newSHA1()
	h.Write([]byte(s))
	return hexOf(h.Sum(nil))
}

// Apagar tem de levar a pasta no identificador.
//
// O envio manda `folder` e `public_id` em campos separados e a Cloudinary
// junta-os. Apagar só com o `public_id` responde **200 com "not found"** — não
// falha, não apaga, e ninguém dá por nada.
func TestApagarLevaAPasta(t *testing.T) {
	var recebido url.Values

	c := cliente(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recebido, _ = url.ParseQuery(string(body))
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	if err := c.DestroyAvatar(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if got := recebido.Get("public_id"); got != "airo/profiles/u1" {
		t.Fatalf("public_id = %q, esperava a pasta incluída", got)
	}
	// A CDN guarda o endereço. Sem isto, a fotografia apagada continuava a ser
	// servida durante horas.
	if recebido.Get("invalidate") != "true" {
		t.Fatal("apagar tem de invalidar a cache")
	}
	if recebido.Get("api_key") != "chave" || recebido.Get("signature") == "" {
		t.Fatal("o pedido tem de ir assinado")
	}
}

// "not found" tem erro próprio: é o resultado que se queria, não uma falha,
// mas um identificador errado não pode passar por sucesso.
func TestApagarOQueNaoExiste(t *testing.T) {
	c := cliente(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":"not found"}`))
	})
	err := c.DestroyAvatar(context.Background(), "u1")
	if !errors.Is(err, cloudinary.ErrNotFound) {
		t.Fatalf("erro = %v, esperava ErrNotFound", err)
	}
}

// Qualquer outro resultado é falha, e diz qual.
func TestApagarComResultadoInesperado(t *testing.T) {
	c := cliente(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":"rate limited"}`))
	})
	err := c.DestroyAvatar(context.Background(), "u1")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("erro = %v", err)
	}
}

// Um prato não se enquadra como uma cara.
//
// `g_face` numa fotografia de comida não encontra nada e cai no centro
// geométrico — que num prato fotografado de cima é a mesa.
func TestEnquadramentoDeRefeicao(t *testing.T) {
	c := cliente(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	up := cloudinary.Uploaded{PublicID: "airo/meals/u1/abc", Version: 9, Format: "jpg"}

	full, thumb := c.MealURLs(up)
	if strings.Contains(full, "g_face") {
		t.Fatalf("comida não se enquadra pela cara: %s", full)
	}
	if !strings.Contains(full, "g_auto") {
		t.Fatalf("o corte tem de seguir o que a imagem tem: %s", full)
	}
	// Afastar um retrato evita a fotografia de passe; afastar um prato só
	// mostra mais mesa.
	if strings.Contains(full, "z_") {
		t.Fatalf("um prato não se afasta: %s", full)
	}
	if !strings.Contains(full, "w_1024,h_1024") || !strings.Contains(thumb, "w_256,h_256") {
		t.Fatalf("tamanhos = %s / %s", full, thumb)
	}
	// E um avatar continua a seguir a cara — os dois não se confundiram.
	avatar, _ := c.AvatarURLs(cloudinary.Uploaded{PublicID: "x", Version: 1, Format: "jpg",
		Faces: [][]int{{1, 2, 3, 4}}})
	if !strings.Contains(avatar, "g_face") {
		t.Fatalf("o avatar perdeu o enquadramento pela cara: %s", avatar)
	}
}
