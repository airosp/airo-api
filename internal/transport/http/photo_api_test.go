package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/airosp/airo-api/internal/service"
)

// uploaderFalso guarda o que recebeu em vez de falar com a Cloudinary.
type uploaderFalso struct {
	recebeu  []byte
	publicID string
	cara     bool
	erro     error
}

func (u *uploaderFalso) UploadAvatar(
	_ context.Context, image []byte, publicID string,
) (string, string, bool, error) {
	if u.erro != nil {
		return "", "", false, u.erro
	}
	u.recebeu = image
	u.publicID = publicID
	corte := "c_fill,g_auto"
	if u.cara {
		corte = "c_fill,g_face,z_0.7"
	}
	base := "https://res.cloudinary.test/savanapoint/image/upload/" + corte
	return base + ",w_512,h_512/v1/airo/profiles/" + publicID + ".jpg",
		base + ",w_128,h_128/v1/airo/profiles/" + publicID + ".jpg", u.cara, nil
}

// jpeg é o menor ficheiro que passa pela verificação dos primeiros bytes.
func jpeg() []byte { return append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x20}, 64)...) }

func enviarFoto(t *testing.T, h http.Handler, nome string, conteudo []byte) *httptest.ResponseRecorder {
	t.Helper()
	var corpo bytes.Buffer
	form := multipart.NewWriter(&corpo)
	parte, err := form.CreateFormFile("photo", nome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parte.Write(conteudo); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/profile/photo", &corpo)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// O caminho completo: a imagem chega, o endereço fica no perfil, e lê-se de
// volta no GET.
func TestFotografiaFicaNoPerfil(t *testing.T) {
	up := &uploaderFalso{cara: true}
	h, _ := serveProfileCom(t, up)

	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatalf("perfil = %d: %s", w.Code, w.Body.String())
	}

	w := enviarFoto(t, h, "retrato.jpg", jpeg())
	if w.Code != http.StatusOK {
		t.Fatalf("envio = %d: %s", w.Code, w.Body.String())
	}

	var saved service.SavedProfile
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.PhotoURL, "g_face") {
		t.Fatalf("com cara, o corte tem de a seguir: %s", saved.PhotoURL)
	}
	if !bytes.Equal(up.recebeu, jpeg()) {
		t.Fatal("os bytes que chegaram não são os que foram enviados")
	}

	// E lê-se de volta: se ficasse só na resposta, a fotografia desaparecia ao
	// reabrir a app.
	r := httptest.NewRequest(http.MethodGet, "/v1/profile", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	var lido service.SavedProfile
	_ = json.Unmarshal(w2.Body.Bytes(), &lido)
	if lido.PhotoURL != saved.PhotoURL {
		t.Fatalf("lido %q ≠ gravado %q", lido.PhotoURL, saved.PhotoURL)
	}
}

// Gravar o perfil outra vez não pode levar a fotografia à frente.
//
// O assistente não manda a foto; sem cuidado, o `ON CONFLICT` punha-a a NULL e
// quem editasse os minutos de treino perdia-a.
func TestGravarOPerfilNaoApagaAFotografia(t *testing.T) {
	h, _ := serveProfileCom(t, &uploaderFalso{cara: true})

	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if w := enviarFoto(t, h, "retrato.jpg", jpeg()); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	outros := strings.Replace(perfilValido, `"workoutMinutes":35`, `"workoutMinutes":50`, 1)
	w := put(t, h, "/v1/profile", outros)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var saved service.SavedProfile
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.PhotoURL == "" {
		t.Fatal("editar o treino apagou a fotografia")
	}
	if saved.WorkoutMinutes != 50 {
		t.Fatalf("minutos = %d", saved.WorkoutMinutes)
	}
}

// O que não é imagem não sai da nossa rede.
func TestOQueNaoEImagemERecusado(t *testing.T) {
	up := &uploaderFalso{}
	h, _ := serveProfileCom(t, up)
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	casos := []struct {
		nome     string
		conteudo []byte
	}{
		{"texto com nome de imagem", []byte("isto não é uma fotografia, é uma frase")},
		{"vazio", nil},
		{"quase JPEG", []byte{0xFF, 0xD8, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			w := enviarFoto(t, h, "foto.jpg", caso.conteudo)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("= %d, esperava 422: %s", w.Code, w.Body.String())
			}
			if up.recebeu != nil {
				t.Fatal("chegou a sair da nossa rede")
			}
		})
	}
}

// Sem perfil não há onde gravar — e dizê-lo é melhor do que um 500.
func TestFotografiaSemPerfil(t *testing.T) {
	h, _ := serveProfileCom(t, &uploaderFalso{})
	w := enviarFoto(t, h, "retrato.jpg", jpeg())
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("= %d, esperava 422: %s", w.Code, w.Body.String())
	}
}

// Sem cara na fotografia, não se finge que há.
func TestSemCaraOCorteMuda(t *testing.T) {
	h, _ := serveProfileCom(t, &uploaderFalso{cara: false})
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	w := enviarFoto(t, h, "paisagem.jpg", jpeg())
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var saved service.SavedProfile
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if strings.Contains(saved.PhotoURL, "g_face") {
		t.Fatalf("sem cara não se usa g_face: %s", saved.PhotoURL)
	}
}

// Apagar tira o endereço do perfil.
func TestApagarFotografia(t *testing.T) {
	h, _ := serveProfileCom(t, &uploaderFalso{cara: true})
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if w := enviarFoto(t, h, "retrato.jpg", jpeg()); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/profile/photo", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("= %d: %s", w.Code, w.Body.String())
	}
	var saved service.SavedProfile
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.PhotoURL != "" {
		t.Fatalf("ainda lá está: %s", saved.PhotoURL)
	}
}

// Uma falha da Cloudinary não deixa o perfil a apontar para nada.
func TestEnvioFalhadoNaoGravaEndereco(t *testing.T) {
	h, _ := serveProfileCom(t, &uploaderFalso{erro: errors.New("a conta está cheia")})
	if w := put(t, h, "/v1/profile", perfilValido); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if w := enviarFoto(t, h, "retrato.jpg", jpeg()); w.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, esperava 500: %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/profile", nil)
	r.Header.Set("Authorization", "Bearer token-de-teste")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var lido service.SavedProfile
	_ = json.Unmarshal(w.Body.Bytes(), &lido)
	if lido.PhotoURL != "" {
		t.Fatalf("gravou um endereço para uma imagem que não existe: %s", lido.PhotoURL)
	}
}
