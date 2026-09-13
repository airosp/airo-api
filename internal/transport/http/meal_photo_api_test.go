package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	airohttp "github.com/airosp/airo-api/internal/transport/http"
	"github.com/airosp/airo-api/internal/transport/http/handlers"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

type mealUploaderFalso struct {
	recebeu   []byte
	publicIDs []string
	erro      error
}

func (u *mealUploaderFalso) UploadMeal(
	_ context.Context, image []byte, publicID string,
) (string, string, error) {
	if u.erro != nil {
		return "", "", u.erro
	}
	u.recebeu = image
	u.publicIDs = append(u.publicIDs, publicID)
	base := "https://res.cloudinary.test/savanapoint/image/upload/c_fill,g_auto,q_auto,f_auto"
	return base + ",w_1024,h_1024/v1/airo/meals/" + publicID + ".jpg",
		base + ",w_256,h_256/v1/airo/meals/" + publicID + ".jpg", nil
}

func serveNutricao(t *testing.T, up handlers.MealUploader) (http.Handler, string) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	const userID = "11111111-2222-3333-4444-555555555555"
	return airohttp.NewRouter(airohttp.Deps{
		Log: quiet, Version: "test",
		Auth:        fakeAuth{userID: userID},
		Nutrition:   &handlers.Nutrition{Photos: up},
		Idempotency: middleware.NewMemoryStore(time.Hour),
	}), userID
}

func enviarRefeicao(t *testing.T, h http.Handler, conteudo []byte) *httptest.ResponseRecorder {
	t.Helper()
	var corpo bytes.Buffer
	form := multipart.NewWriter(&corpo)
	parte, err := form.CreateFormFile("photo", "prato.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parte.Write(conteudo); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/nutrition/meal-photo", &corpo)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestFotografiaDeRefeicaoDevolveDoisTamanhos(t *testing.T) {
	up := &mealUploaderFalso{}
	h, userID := serveNutricao(t, up)

	w := enviarRefeicao(t, h, jpeg())
	if w.Code != http.StatusOK {
		t.Fatalf("= %d: %s", w.Code, w.Body.String())
	}

	var out handlers.MealPhotoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.URL, "w_1024") || !strings.Contains(out.ThumbURL, "w_256") {
		t.Fatalf("tamanhos = %s / %s", out.URL, out.ThumbURL)
	}
	// A fotografia fica dentro da pasta de quem a tirou: é o que permite saber
	// de quem é sem consultar nada, e apagar tudo o que é de alguém.
	if !strings.HasPrefix(up.publicIDs[0], userID+"/") {
		t.Fatalf("identificador = %q, esperava começar pelo utilizador", up.publicIDs[0])
	}
	if !bytes.Equal(up.recebeu, jpeg()) {
		t.Fatal("os bytes que chegaram não são os que foram enviados")
	}
}

// Duas refeições não podem partilhar nome.
//
// De uma refeição há muitas — ao contrário de um avatar, que é um por pessoa.
// Um identificador estável faria a segunda fotografia apagar a primeira, e o
// registo de ontem passava a mostrar o jantar de hoje.
func TestCadaRefeicaoTemNomeProprio(t *testing.T) {
	up := &mealUploaderFalso{}
	h, _ := serveNutricao(t, up)

	for i := 0; i < 5; i++ {
		if w := enviarRefeicao(t, h, jpeg()); w.Code != http.StatusOK {
			t.Fatalf("envio %d = %d", i, w.Code)
		}
	}
	vistos := map[string]bool{}
	for _, id := range up.publicIDs {
		if vistos[id] {
			t.Fatalf("identificador repetido: %s", id)
		}
		vistos[id] = true
	}
	if len(vistos) != 5 {
		t.Fatalf("%d identificadores distintos em 5 envios", len(vistos))
	}
}

// As mesmas regras da fotografia de perfil, porque a leitura é a mesma.
func TestRefeicaoRecusaOQueNaoEImagem(t *testing.T) {
	up := &mealUploaderFalso{}
	h, _ := serveNutricao(t, up)

	w := enviarRefeicao(t, h, []byte("isto é uma frase, não um prato"))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("= %d, esperava 422: %s", w.Code, w.Body.String())
	}
	if up.recebeu != nil {
		t.Fatal("chegou a sair da nossa rede")
	}
}

func TestRefeicaoSemSessao(t *testing.T) {
	h, _ := serveNutricao(t, &mealUploaderFalso{})
	r := httptest.NewRequest(http.MethodPost, "/v1/nutrition/meal-photo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("= %d, esperava 401", w.Code)
	}
}

func TestRefeicaoComFalhaNaCloudinary(t *testing.T) {
	h, _ := serveNutricao(t, &mealUploaderFalso{erro: errors.New("a conta está cheia")})
	if w := enviarRefeicao(t, h, jpeg()); w.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, esperava 500", w.Code)
	}
}
