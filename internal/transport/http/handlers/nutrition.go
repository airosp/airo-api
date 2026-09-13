package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// MealUploader guarda a fotografia de uma refeição e devolve os endereços já
// enquadrados.
type MealUploader interface {
	UploadMeal(ctx context.Context, image []byte, publicID string) (full, thumb string, err error)
}

type Nutrition struct {
	Photos MealUploader
}

// MealPhotoResponse é o que a app recebe.
//
// Dois tamanhos porque são dois sítios: a fotografia grande abre-se quando se
// toca no registo, a pequena vive na lista de "o que comi ultimamente". Pedir a
// de 1024 para desenhar a 64 gasta dezasseis vezes mais bytes.
type MealPhotoResponse struct {
	URL      string `json:"url"`
	ThumbURL string `json:"thumbUrl"`
}

// MealPhoto recebe a fotografia de uma refeição fora do plano.
//
// ⚠️ **Não grava registo nenhum.** O diário alimentar ainda vive no telemóvel;
// aqui só se guarda a imagem e se devolve o endereço. Quando o diário passar
// para o servidor, é esta resposta que lhe fica ligada — e só aí a imagem deixa
// de poder ficar órfã se alguém apagar os dados da app.
func (h Nutrition) MealPhoto(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Photos == nil {
		apierr.Write(w, apierr.Internal, "As fotografias estão indisponíveis.", "")
		return
	}

	image, erro := lerImagem(w, r)
	if erro != "" {
		apierr.Write(w, apierr.ValidationFailed, erro, "photo")
		return
	}

	// Um nome por fotografia, e não por pessoa: de uma refeição há muitas, e um
	// identificador estável faria a segunda apagar a primeira.
	//
	// Dentro da pasta da pessoa, para se saber de quem é sem consultar nada —
	// e para apagar tudo o que é de alguém ser um prefixo.
	nome, err := nomeAleatorio()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível guardar a fotografia.")
		return
	}

	full, thumb, err := h.Photos.UploadMeal(r.Context(), image, userID+"/"+nome)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível guardar a fotografia.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, MealPhotoResponse{URL: full, ThumbURL: thumb})
}

// lerImagem lê e valida o ficheiro do formulário.
//
// Partilhada com a fotografia de perfil: as mesmas regras, e uma cópia delas
// seria uma das duas a ficar para trás.
func lerImagem(w http.ResponseWriter, r *http.Request) ([]byte, string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+(1<<20))
	if err := r.ParseMultipartForm(maxPhotoBytes); err != nil {
		return nil, "Fotografia demasiado grande ou ilegível."
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, header, err := r.FormFile("photo")
	if err != nil {
		return nil, "Falta a fotografia."
	}
	defer func() { _ = file.Close() }()

	if header.Size > maxPhotoBytes {
		return nil, "A fotografia não pode passar de 8 MB."
	}
	image, err := io.ReadAll(io.LimitReader(file, maxPhotoBytes+1))
	if err != nil {
		return nil, "Não foi possível ler a fotografia."
	}
	if len(image) > maxPhotoBytes {
		return nil, "A fotografia não pode passar de 8 MB."
	}
	// O tipo sai dos bytes, não do que o cliente disse que era.
	if !imagemAceite(image) {
		return nil, "Envia uma imagem JPEG, PNG, WebP ou HEIC."
	}
	return image, ""
}

func nomeAleatorio() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("gerar nome: " + err.Error())
	}
	return hex.EncodeToString(b), nil
}
