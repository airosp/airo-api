package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
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
