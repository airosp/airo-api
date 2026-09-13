package dto

// MealLogRequest é um registo do diário alimentar, como o telemóvel o tem.
//
// O identificador não vem no corpo: vem no caminho. É o telemóvel que o dá, e
// pô-lo no URL torna a operação idempotente por construção — mandar o mesmo
// registo duas vezes é mandar o mesmo registo.
type MealLogRequest struct {
	Slot   string `json:"slot"`
	Status string `json:"status"`
	// Portion é a fracção consumida, 0 a 1. **Zero é um registo válido**: "não
	// comi" não é o mesmo que "não registei".
	Portion float64 `json:"portion"`
	Kcal    int     `json:"kcal"`
	Macros  struct {
		Protein float64 `json:"protein"`
		Carbs   float64 `json:"carbs"`
		Fat     float64 `json:"fat"`
	} `json:"macros"`
	Source string `json:"source"`

	Label         string `json:"label,omitempty"`
	PortionLabel  string `json:"portionLabel,omitempty"`
	PhotoURL      string `json:"photoUrl,omitempty"`
	PhotoThumbURL string `json:"photoThumbUrl,omitempty"`

	RecordedAt string `json:"recordedAt"`
	// LocalDay é o dia **da pessoa**, não o do servidor. Quem janta à meia-noite
	// e um quarto jantou ontem, e um `date_trunc` em UTC diria outra coisa.
	LocalDay string `json:"localDay"`
}

// MealLogResponse é um registo como o servidor o devolve.
type MealLogResponse struct {
	ID      string  `json:"id"`
	Slot    string  `json:"slot"`
	Status  string  `json:"status"`
	Portion float64 `json:"portion"`
	Kcal    int     `json:"kcal"`
	Macros  struct {
		Protein float64 `json:"protein"`
		Carbs   float64 `json:"carbs"`
		Fat     float64 `json:"fat"`
	} `json:"macros"`
	Source string `json:"source"`

	Label         string `json:"label,omitempty"`
	PortionLabel  string `json:"portionLabel,omitempty"`
	PhotoURL      string `json:"photoUrl,omitempty"`
	PhotoThumbURL string `json:"photoThumbUrl,omitempty"`

	RecordedAt string `json:"recordedAt"`
	LocalDay   string `json:"localDay"`
}

type MealLogsResponse struct {
	Logs []MealLogResponse `json:"logs"`
}
