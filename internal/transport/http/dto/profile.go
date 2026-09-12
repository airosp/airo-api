package dto

// ProfileRequest é o que a app manda no fim do onboarding.
//
// Os valores de lista são validados **aqui** e não deixados ao `CHECK` do
// esquema: um enum recusado pela base de dados chega ao cliente como 500, e
// 500 quer dizer "a culpa é nossa". Isto é do pedido, e diz-se qual o campo.
type ProfileRequest struct {
	DisplayName string   `json:"displayName"`
	BirthDate   *string  `json:"birthDate,omitempty"` // AAAA-MM-DD
	Sex         string   `json:"sex"`
	HeightCm    float64  `json:"heightCm"`
	WeightKg    *float64 `json:"weightKg,omitempty"`

	Experience     string   `json:"experience"`
	WorkoutDays    []int    `json:"workoutDays"`
	WorkoutMinutes int      `json:"workoutMinutes"`
	WorkoutTime    string   `json:"workoutTime"`
	Equipment      []string `json:"equipment"`

	DietStyle      string   `json:"dietStyle"`
	MealsPerDay    int      `json:"mealsPerDay"`
	FoodBudget     string   `json:"foodBudget"`
	FoodExclusions []string `json:"foodExclusions"`
}
