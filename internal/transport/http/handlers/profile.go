package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

type Profile struct {
	Profiles *service.Profiles
	Clock    clock.Clock
}

func (h Profile) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	out, err := h.Profiles.Read(r.Context(), userID)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.NotFound, "Ainda não tens perfil.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler o perfil.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// maxPhotoBytes é o limite do que se aceita.
//
// Oito megabytes é mais do que qualquer fotografia de perfil precisa e menos do
// que chega para encher a memória do servidor. Sem limite, um pedido escolhe
// quanta memória nos gasta.
const maxPhotoBytes = 8 << 20

// Photo recebe a fotografia e devolve o perfil com o endereço já enquadrado.
//
// A imagem passa **por aqui** e não do telemóvel directamente para a Cloudinary:
// uma chave de envio no cliente é uma chave pública. É também o que permite
// recusar o que não é imagem antes de sair da nossa rede.
func (h Profile) Photo(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	image, erro := lerImagem(w, r)
	if erro != "" {
		apierr.Write(w, apierr.ValidationFailed, erro, "photo")
		return
	}

	out, err := h.Profiles.SavePhoto(r.Context(), userID, image, h.Clock.Now())
	switch {
	case errors.Is(err, service.ErrNoUploader):
		apierr.Write(w, apierr.Internal, "As fotografias estão indisponíveis.", "")
		return
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Cria o teu perfil primeiro.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível guardar a fotografia.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// DeletePhoto tira a fotografia do perfil.
func (h Profile) DeletePhoto(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	out, err := h.Profiles.RemovePhoto(r.Context(), userID, h.Clock.Now())
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.NotFound, "Ainda não tens perfil.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível remover a fotografia.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// imagemAceite reconhece o formato pelos primeiros bytes.
//
// São os quatro que um telemóvel produz. O HEIC do iPhone entra porque é o que
// a câmara grava por omissão — recusá-lo seria recusar metade dos telemóveis.
func imagemAceite(b []byte) bool {
	switch {
	case len(b) < 12:
		return false
	// JPEG: FF D8 FF
	case b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return true
	// PNG: 89 P N G
	case b[0] == 0x89 && string(b[1:4]) == "PNG":
		return true
	// WebP: RIFF ---- WEBP
	case string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return true
	// HEIC/HEIF: ---- ftyp + marca
	case string(b[4:8]) == "ftyp":
		marca := string(b[8:12])
		return marca == "heic" || marca == "heix" || marca == "hevc" ||
			marca == "mif1" || marca == "msf1" || marca == "heim" || marca == "heis"
	}
	return false
}

func (h Profile) Put(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	var req dto.ProfileRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	in, field, msg := validateProfile(req)
	if field != "" {
		apierr.Write(w, apierr.ValidationFailed, msg, field)
		return
	}

	out, err := h.Profiles.Save(r.Context(), userID, in, h.Clock.Now())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o perfil.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

// validateProfile devolve o input pronto, ou o campo e a mensagem do que está
// errado. Um erro de cada vez: dizer cinco coisas ao mesmo tempo num ecrã de
// telemóvel não ajuda ninguém a corrigir a primeira.
func validateProfile(req dto.ProfileRequest) (service.SaveProfileInput, string, string) {
	in := service.SaveProfileInput{
		DisplayName:    trim(req.DisplayName),
		Sex:            or(req.Sex, "unspecified"),
		HeightCm:       req.HeightCm,
		WeightKg:       req.WeightKg,
		Experience:     or(req.Experience, "beginner"),
		WorkoutDays:    req.WorkoutDays,
		WorkoutMinutes: req.WorkoutMinutes,
		WorkoutTime:    or(req.WorkoutTime, "evening"),
		Equipment:      req.Equipment,
		DietStyle:      or(req.DietStyle, "omnivore"),
		MealsPerDay:    req.MealsPerDay,
		FoodBudget:     or(req.FoodBudget, "medium"),
		FoodExclusions: req.FoodExclusions,
		Preferences:    preferenciasDe(req),
	}

	if in.DisplayName == "" {
		return in, "displayName", "Diz-nos como te chamas."
	}
	if !oneOf(in.Sex, "female", "male", "unspecified") {
		return in, "sex", "Sexo desconhecido."
	}
	// 90 cm e 250 cm não são limites clínicos — são o que separa um engano de
	// digitação de uma pessoa. Fora disto, o mais provável é ter faltado uma
	// tecla.
	if in.HeightCm < 90 || in.HeightCm > 250 {
		return in, "heightCm", "Altura fora do esperado."
	}
	if in.WeightKg != nil && (*in.WeightKg < 25 || *in.WeightKg > 400) {
		return in, "weightKg", "Peso fora do esperado."
	}
	if !oneOf(in.Experience, "beginner", "intermediate", "advanced") {
		return in, "experience", "Nível desconhecido."
	}
	if !oneOf(in.WorkoutTime, "morning", "midday", "evening") {
		return in, "workoutTime", "Altura do dia desconhecida."
	}
	if !oneOf(in.DietStyle, "omnivore", "high_protein", "vegetarian", "vegan") {
		return in, "dietStyle", "Estilo alimentar desconhecido."
	}
	if !oneOf(in.FoodBudget, "low", "medium", "high") {
		return in, "foodBudget", "Orçamento desconhecido."
	}
	// O esquema exige entre 3 e 5. Sem isto, 6 refeições saíam como 500.
	if in.MealsPerDay < 3 || in.MealsPerDay > 5 {
		return in, "mealsPerDay", "Escolhe entre 3 e 5 refeições."
	}
	if len(in.WorkoutDays) == 0 {
		return in, "workoutDays", "Escolhe pelo menos um dia."
	}
	for _, d := range in.WorkoutDays {
		if d < 0 || d > 6 {
			return in, "workoutDays", "Dia da semana inválido."
		}
	}
	if in.WorkoutMinutes < 10 || in.WorkoutMinutes > 240 {
		return in, "workoutMinutes", "Duração fora do esperado."
	}

	if req.Age != nil {
		if *req.Age < 13 || *req.Age > 120 {
			return in, "age", "Idade fora do esperado."
		}
		in.AgeYears = req.Age
	}

	if req.BirthDate != nil && *req.BirthDate != "" {
		born, err := time.Parse("2006-01-02", *req.BirthDate)
		if err != nil {
			return in, "birthDate", "Data de nascimento inválida."
		}
		if born.After(time.Now().UTC()) {
			return in, "birthDate", "Data de nascimento no futuro."
		}
		in.BirthDate = &born
	}

	if in.BirthDate == nil && in.AgeYears == nil {
		// Os motores precisam da idade para o metabolismo basal. Sem ela não
		// há conta nenhuma a fazer, e adivinhar seria pior do que pedir.
		return in, "age", "Diz-nos a tua idade."
	}

	if in.Equipment == nil {
		in.Equipment = []string{}
	}
	if in.FoodExclusions == nil {
		in.FoodExclusions = []string{}
	}
	return in, "", ""
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func trim(v string) string { return strings.TrimSpace(v) }

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// preferenciasDe traduz as preferências de exercício, quando o pedido as traz.
//
// Devolve nil quando **nenhuma** das três vem — ausente é "não mexer". Basta
// uma delas para o conjunto ser substituído: mandar só os fixados e ficar com
// os excluídos antigos daria um estado que ninguém pediu.
func preferenciasDe(req dto.ProfileRequest) *repo.Preferences {
	if req.Pinned == nil && req.Excluded == nil && req.Prescriptions == nil {
		return nil
	}
	out := repo.Preferences{
		Pinned:        []string{},
		Excluded:      []string{},
		Prescriptions: map[string]training.Prescription{},
	}
	if req.Pinned != nil {
		out.Pinned = *req.Pinned
	}
	if req.Excluded != nil {
		out.Excluded = *req.Excluded
	}
	if req.Prescriptions != nil {
		for id, pres := range *req.Prescriptions {
			// Um exercício sem séries não é um exercício — invariante 13. Aqui
			// ignora-se em silêncio em vez de recusar o perfil todo: a
			// prescrição é um extra, e recusar a gravação do nome e do peso por
			// causa de um número mal formado seria desproporcionado.
			if pres.Sets < 1 || pres.Target < 1 {
				continue
			}
			out.Prescriptions[id] = training.Prescription{Sets: pres.Sets, Target: pres.Target}
		}
	}
	return &out
}

// Account é a eliminação da conta.
//
// Fica no mesmo handler que o perfil porque é a mesma coisa vista do outro
// lado: um cria a pessoa no sistema, o outro tira-a.
type Account struct {
	Service *service.AccountService
}

// Delete apaga a conta e tudo o que lhe pertence.
//
// `DELETE /v1/account` e não `POST /account/delete`: é uma eliminação, e o
// método diz-lho. Sem corpo e sem confirmação aqui — a confirmação é do ecrã,
// que é onde a pessoa está, e pedi-la também no protocolo dava a entender que
// uma delas pode ser saltada.
func (h Account) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Service == nil {
		apierr.Write(w, apierr.Internal, "A eliminação de conta está indisponível.", "")
		return
	}

	out, err := h.Service.Delete(r.Context(), userID)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		// Já não existe. Quem pediu para apagar queria que deixasse de existir,
		// e deixou — responder 404 punha a pessoa a duvidar se ficou feito.
		w.WriteHeader(http.StatusNoContent)
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível apagar a tua conta.")
		return
	}

	// O que se regista é o que aconteceu, nunca a quem: o pedido já não tem
	// dono a partir daqui, e o identificador estaria a apontar para nada.
	_ = out
	w.WriteHeader(http.StatusNoContent)
}
