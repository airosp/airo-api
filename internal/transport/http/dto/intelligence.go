package dto

import ai "github.com/airosp/airo-api/internal/service/intelligence"

// ConsentRequest dá ou retira uma autorização.
//
// `Granted` é um ponteiro: um corpo sem ele não é "não", é um pedido mal
// feito — e confundir os dois revogava uma autorização por esquecimento.
type ConsentRequest struct {
	Purpose       string `json:"purpose"`
	Granted       *bool  `json:"granted"`
	PolicyVersion string `json:"policyVersion,omitempty"`
}

type ConsentsResponse struct {
	Consents []ai.ConsentState `json:"consents"`
}

// RecognizeMealRequest pede o reconhecimento de uma fotografia já guardada por
// `POST /v1/nutrition/meal-photo`.
type RecognizeMealRequest struct {
	PhotoURL      string `json:"photoUrl"`
	PhotoThumbURL string `json:"photoThumbUrl,omitempty"`
	Slot          string `json:"slot"`
	LocalDay      string `json:"localDay"`
}

// ParseMealRequest pede a interpretação de um registo escrito.
type ParseMealRequest struct {
	Text     string `json:"text"`
	Slot     string `json:"slot"`
	LocalDay string `json:"localDay"`
}
