package dto

// ProfileRequest é o que a app manda no fim do onboarding.
//
// Os valores de lista são validados **aqui** e não deixados ao `CHECK` do
// esquema: um enum recusado pela base de dados chega ao cliente como 500, e
// 500 quer dizer "a culpa é nossa". Isto é do pedido, e diz-se qual o campo.
type ProfileRequest struct {
	DisplayName string  `json:"displayName"`
	BirthDate   *string `json:"birthDate,omitempty"` // AAAA-MM-DD
	// Age é a alternativa a BirthDate: a app pergunta a idade, não o
	// aniversário. Quando vierem os dois, a data manda.
	Age      *int     `json:"age,omitempty"`
	Sex      string   `json:"sex"`
	HeightCm float64  `json:"heightCm"`
	WeightKg *float64 `json:"weightKg,omitempty"`

	Experience     string   `json:"experience"`
	WorkoutDays    []int    `json:"workoutDays"`
	WorkoutMinutes int      `json:"workoutMinutes"`
	WorkoutTime    string   `json:"workoutTime"`
	Equipment      []string `json:"equipment"`

	DietStyle      string   `json:"dietStyle"`
	MealsPerDay    int      `json:"mealsPerDay"`
	FoodBudget     string   `json:"foodBudget"`
	FoodExclusions []string `json:"foodExclusions"`

	// As preferências de exercício são **ponteiros** de propósito.
	//
	// O resto do perfil é estado: mandá-lo substitui o que lá estava. Estas
	// chegaram depois, e um cliente antigo que não as envie estaria a dizer
	// "não tenho nenhuma" sem o querer dizer — apagava as escolhas de quem
	// ainda não tinha actualizado a app. Ausente é diferente de vazio: ausente
	// não mexe, vazio limpa.
	Pinned        *[]string                   `json:"pinnedExercises,omitempty"`
	Excluded      *[]string                   `json:"excludedExercises,omitempty"`
	Prescriptions *map[string]PrescriptionDTO `json:"exercisePrescriptions,omitempty"`
}

// PrescriptionDTO são as séries e o alvo que a pessoa fixou para um exercício.
type PrescriptionDTO struct {
	Sets int `json:"sets"`
	// Target são repetições por série, ou segundos quando a medida é tempo.
	Target int `json:"target"`
}
