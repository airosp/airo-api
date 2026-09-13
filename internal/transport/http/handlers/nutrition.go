package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
	"github.com/airosp/airo-api/internal/transport/http/view"
)

// MealUploader guarda a fotografia de uma refeição e devolve os endereços já
// enquadrados.
type MealUploader interface {
	UploadMeal(ctx context.Context, image []byte, publicID string) (full, thumb string, err error)
}

// MealLogStore guarda o diário alimentar.
type MealLogStore interface {
	SaveLog(ctx context.Context, userID string, l repo.MealLog) error
	Logs(ctx context.Context, userID string, from, to time.Time) ([]repo.MealLog, error)
	DeleteLog(ctx context.Context, userID, clientID string) (bool, error)
}

type Nutrition struct {
	Photos   MealUploader
	Logs     MealLogStore
	Plans    *service.NutritionService
	Profiles NutritionProfileReader
}

// SaveLog grava um registo do diário.
//
// `PUT` com o identificador no caminho, e não `POST`: o registo nasce no
// telemóvel, muitas vezes sem rede, e é ele que lhe dá o nome. Mandar o mesmo
// registo duas vezes tem de ser mandar o mesmo registo — numa rede fraca isso
// acontece sempre, e com `POST` o almoço aparecia três vezes.
func (h Nutrition) SaveLog(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Logs == nil {
		apierr.Write(w, apierr.Internal, "O diário está indisponível.", "")
		return
	}

	clientID := strings.TrimSpace(r.PathValue("id"))
	if clientID == "" || len(clientID) > 128 {
		apierr.Write(w, apierr.ValidationFailed, "Identificador do registo inválido.", "id")
		return
	}

	var req dto.MealLogRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	log, campo, msg := validarRegisto(clientID, req)
	if campo != "" {
		apierr.Write(w, apierr.ValidationFailed, msg, campo)
		return
	}

	if err := h.Logs.SaveLog(r.Context(), userID, log); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o registo.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, paraResposta(log))
}

// ReadLogs devolve os registos de um intervalo.
func (h Nutrition) ReadLogs(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Logs == nil {
		apierr.Write(w, apierr.Internal, "O diário está indisponível.", "")
		return
	}

	from, err := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data inicial inválida.", "from")
		return
	}
	to, err := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data final inválida.", "to")
		return
	}
	if to.Before(from) {
		apierr.Write(w, apierr.ValidationFailed, "O fim é antes do início.", "to")
		return
	}
	// Um intervalo sem limite é um pedido que fica cada vez mais lento à medida
	// que a pessoa usa a app. Um ano chega para qualquer ecrã que exista.
	if to.Sub(from) > 366*24*time.Hour {
		apierr.Write(w, apierr.ValidationFailed, "Pede no máximo um ano de cada vez.", "to")
		return
	}

	logs, err := h.Logs.Logs(r.Context(), userID, from, to)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o diário.")
		return
	}

	out := dto.MealLogsResponse{Logs: make([]dto.MealLogResponse, 0, len(logs))}
	for _, l := range logs {
		out.Logs = append(out.Logs, paraResposta(l))
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// DeleteLog apaga um registo.
//
// Responde 204 exista ou não: apagar o que já não está é o resultado que se
// queria, e obrigar o telemóvel a distinguir os dois casos só lhe dava trabalho
// para chegar à mesma conclusão.
func (h Nutrition) DeleteLog(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Logs == nil {
		apierr.Write(w, apierr.Internal, "O diário está indisponível.", "")
		return
	}
	if _, err := h.Logs.DeleteLog(r.Context(), userID, r.PathValue("id")); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível apagar o registo.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validarRegisto(clientID string, req dto.MealLogRequest) (repo.MealLog, string, string) {
	l := repo.MealLog{
		ClientID: clientID,
		Slot:     req.Slot,
		Status:   req.Status,
		Portion:  req.Portion,
		Kcal:     req.Kcal,
		Protein:  req.Macros.Protein,
		Carbs:    req.Macros.Carbs,
		Fat:      req.Macros.Fat,
		Source:   req.Source,
	}

	if !oneOf(l.Slot, "breakfast", "lunch", "snack", "dinner", "supper") {
		return l, "slot", "Refeição desconhecida."
	}
	if !oneOf(l.Status, "planned", "eaten", "partial", "skipped", "substituted", "custom") {
		return l, "status", "Estado desconhecido."
	}
	if !oneOf(l.Source, "plan", "manual", "photo") {
		return l, "source", "Origem desconhecida."
	}
	// O esquema tem um CHECK entre 0 e 1. Sem validar aqui, 1.5 saía como 500.
	if l.Portion < 0 || l.Portion > 1 {
		return l, "portion", "A fracção consumida vai de 0 a 1."
	}
	if l.Kcal < 0 || l.Kcal > 20000 {
		return l, "kcal", "Calorias fora do esperado."
	}
	for campo, v := range map[string]float64{
		"macros.protein": l.Protein, "macros.carbs": l.Carbs, "macros.fat": l.Fat,
	} {
		if v < 0 || v > 2000 {
			return l, campo, "Macronutriente fora do esperado."
		}
	}

	at, err := time.Parse(time.RFC3339, req.RecordedAt)
	if err != nil {
		return l, "recordedAt", "Momento do registo inválido."
	}
	l.RecordedAt = at

	dia, err := time.Parse("2006-01-02", req.LocalDay)
	if err != nil {
		return l, "localDay", "Dia inválido."
	}
	l.LocalDay = dia

	l.Label = opcional(req.Label)
	l.PortionLabel = opcional(req.PortionLabel)
	l.PhotoURL = opcional(req.PhotoURL)
	l.PhotoThumb = opcional(req.PhotoThumbURL)
	return l, "", ""
}

func paraResposta(l repo.MealLog) dto.MealLogResponse {
	out := dto.MealLogResponse{
		ID: l.ClientID, Slot: l.Slot, Status: l.Status, Portion: l.Portion,
		Kcal: l.Kcal, Source: l.Source,
		Label: deref(l.Label), PortionLabel: deref(l.PortionLabel),
		PhotoURL: deref(l.PhotoURL), PhotoThumbURL: deref(l.PhotoThumb),
		RecordedAt: l.RecordedAt.UTC().Format(time.RFC3339),
		LocalDay:   l.LocalDay.Format("2006-01-02"),
	}
	out.Macros.Protein = l.Protein
	out.Macros.Carbs = l.Carbs
	out.Macros.Fat = l.Fat
	return out
}

func opcional(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
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

// NutritionProfileReader dá ao handler o que o motor precisa e o pedido não traz.
type NutritionProfileReader interface {
	NutritionProfile(ctx contextLike, userID string, day time.Time) (service.NutritionTodayInput, error)
}

// Today devolve o plano alimentar do dia, já decidido.
//
// O alvo calórico, os macros, a repartição pelas refeições e os alimentos de
// cada uma são decisões — e nenhuma delas volta a ser tomada no telemóvel. O
// cliente desenha os cartões que aqui vão.
func (h Nutrition) Today(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Plans == nil || h.Profiles == nil {
		apierr.Write(w, apierr.Internal, "O plano alimentar está indisponível.", "")
		return
	}

	day, err := localDay(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}

	in, err := h.Profiles.NutritionProfile(r.Context(), userID, day)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	case errors.Is(err, service.ErrWeightMissing):
		// Ao contrário do treino, aqui o peso faz falta a sério: sem ele não há
		// proteína por quilo nem gasto. Dizer o que falta é melhor do que
		// devolver um plano inventado.
		apierr.Write(w, apierr.ValidationFailed, "Falta o teu peso para calcular o plano alimentar.", "weightKg")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return
	}

	out, err := h.Plans.Today(r.Context(), in)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível montar o plano de hoje.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, view.BuildNutritionDay(
		out.Strategy, out.Day, in.TrainsToday, out.FromStoredStrategy, out.Swapped))
}

// SwapMeal troca uma refeição por outra proposta.
//
// `POST` e não `PUT`: cada toque é um pedido novo — "mostra-me outra" — e não a
// declaração de um estado. Repetir dá coisa diferente de propósito, que é
// exactamente o que quem carrega quer.
func (h Nutrition) SwapMeal(w http.ResponseWriter, r *http.Request) {
	h.mexerNaRefeicao(w, r, true)
}

// ResetMeal devolve a refeição à proposta do plano.
func (h Nutrition) ResetMeal(w http.ResponseWriter, r *http.Request) {
	h.mexerNaRefeicao(w, r, false)
}

var slotsValidos = map[string]bool{
	"breakfast": true, "lunch": true, "snack": true, "dinner": true, "supper": true,
}

func (h Nutrition) mexerNaRefeicao(w http.ResponseWriter, r *http.Request, trocar bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Plans == nil || h.Profiles == nil {
		apierr.Write(w, apierr.Internal, "O plano alimentar está indisponível.", "")
		return
	}

	slot := r.PathValue("slot")
	if !slotsValidos[slot] {
		// Um enum recusado pela base de dados chega ao cliente como 500, e 500
		// quer dizer "a culpa é nossa". Isto é do pedido, e diz-se o campo.
		apierr.Write(w, apierr.ValidationFailed, "Refeição desconhecida.", "slot")
		return
	}

	day, err := localDay(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}

	in, err := h.Profiles.NutritionProfile(r.Context(), userID, day)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return
	}

	var out service.NutritionToday
	if trocar {
		out, err = h.Plans.SwapMeal(r.Context(), in, slot)
	} else {
		out, err = h.Plans.ResetMeal(r.Context(), in, slot)
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível mudar a refeição.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, view.BuildNutritionDay(
		out.Strategy, out.Day, in.TrainsToday, out.FromStoredStrategy, out.Swapped))
}
