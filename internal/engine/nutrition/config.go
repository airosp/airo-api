// Package nutrition é o Nutrition Engine. Puro: recebe um instantâneo, devolve
// uma decisão. Não lê da base de dados nem sabe que existe HTTP.
package nutrition

import (
	"encoding/json"
	"fmt"
	"io"
)

// ⚠️ Nenhum destes limiares está validado clinicamente.
//
// São referências comuns da literatura, usadas para o produto funcionar. Têm de
// ser revistos e assinados por um nutricionista antes de qualquer lançamento —
// e é por isso que são **dados** e não constantes compiladas: quando um
// profissional corrigir um valor, muda a configuração, não a aplicação.
//
// Os valores por omissão são os que o cliente usa hoje, em
// mobile/modules/nutrition-engine/domain/config.ts. Portar é copiá-los.

type GoalType string

const (
	LoseFat     GoalType = "lose_fat"
	GainMuscle  GoalType = "gain_muscle"
	Maintain    GoalType = "maintain"
	Performance GoalType = "performance"
	Health      GoalType = "health"
)

type Slot string

const (
	Breakfast Slot = "breakfast"
	Lunch     Slot = "lunch"
	Snack     Slot = "snack"
	Dinner    Slot = "dinner"
	Supper    Slot = "supper"
)

type Band struct {
	Comfortable float64 `json:"comfortable"`
	Max         float64 `json:"max"`
}

type MacroShift struct {
	Protein float64 `json:"protein"`
	Carbs   float64 `json:"carbs"`
	Fat     float64 `json:"fat"`
}

type WorkoutMeals struct {
	Pre  Slot `json:"pre"`
	Post Slot `json:"post"`
}

type Assessment struct {
	MinLoggedDays          int     `json:"minLoggedDays"`
	CalorieTolerance       float64 `json:"calorieTolerance"`
	ProteinTolerance       float64 `json:"proteinTolerance"`
	RecalibrationThreshold float64 `json:"recalibrationThreshold"`
	StrongAdherence        float64 `json:"strongAdherence"`
	WeakAdherence          float64 `json:"weakAdherence"`
}

type Adaptation struct {
	StepKcal     int     `json:"stepKcal"`
	MaxStepRatio float64 `json:"maxStepRatio"`
}

type Config struct {
	Version          string               `json:"version"`
	EnergyAdjustment map[GoalType]Band    `json:"energyAdjustment"`
	MinDailyCalories int                  `json:"minDailyCalories"`
	Protein          map[GoalType]float64 `json:"protein"`
	MinFatRatio      float64              `json:"minFatRatio"`
	MinCarbsG        int                  `json:"minCarbsG"`
	MealSplit        map[int][]float64    `json:"mealSplit"`
	// Hydration: quanta água por dia. Ver `hydration.go` — nenhum destes
	// números está validado clinicamente.
	Hydration struct {
		MlPerKg           float64 `json:"mlPerKg"`
		MlPerTrainingHour float64 `json:"mlPerTrainingHour"`
		MinMl             int     `json:"minMl"`
		MaxMl             int     `json:"maxMl"`
		// FallbackMl é o que se diz quando não se sabe o peso. Não é um alvo:
		// é a recusa honesta de calcular sem o dado que a conta precisa.
		FallbackMl int `json:"fallbackMl"`
	} `json:"hydration"`

	SlotsByCount   map[int][]Slot          `json:"slotsByCount"`
	RoleMacroShift map[string]MacroShift   `json:"roleMacroShift"`
	WorkoutMeals   map[string]WorkoutMeals `json:"workoutMeals"`
	KcalPerKg      float64                 `json:"kcalPerKg"`
	Assessment     Assessment              `json:"assessment"`
	Adaptation     Adaptation              `json:"adaptation"`
	CycleWeeks     int                     `json:"cycleWeeks"`
}

// DefaultConfig são os valores em vigor no cliente. Servem de omissão para a
// API arrancar sem configuração carregada — não para dispensar a revisão.
func DefaultConfig() Config {
	return Config{
		Version: "2026-09-12",
		EnergyAdjustment: map[GoalType]Band{
			LoseFat:     {Comfortable: -0.12, Max: -0.20},
			GainMuscle:  {Comfortable: 0.08, Max: 0.15},
			Maintain:    {Comfortable: 0, Max: 0},
			Performance: {Comfortable: 0.03, Max: 0.08},
			Health:      {Comfortable: 0, Max: 0.05},
		},
		MinDailyCalories: 1500,
		Protein: map[GoalType]float64{
			LoseFat: 2.0, GainMuscle: 2.0, Maintain: 1.6, Performance: 1.8, Health: 1.4,
		},
		MinFatRatio: 0.22,
		MinCarbsG:   80,
		MealSplit: map[int][]float64{
			3: {0.30, 0.40, 0.30},
			4: {0.25, 0.35, 0.15, 0.25},
			5: {0.22, 0.30, 0.13, 0.25, 0.10},
		},
		Hydration: struct {
			MlPerKg           float64 `json:"mlPerKg"`
			MlPerTrainingHour float64 `json:"mlPerTrainingHour"`
			MinMl             int     `json:"minMl"`
			MaxMl             int     `json:"maxMl"`
			FallbackMl        int     `json:"fallbackMl"`
		}{MlPerKg: 35, MlPerTrainingHour: 500, MinMl: 1500, MaxMl: 4000, FallbackMl: 2000},
		SlotsByCount: map[int][]Slot{
			3: {Breakfast, Lunch, Dinner},
			4: {Breakfast, Lunch, Snack, Dinner},
			5: {Breakfast, Lunch, Snack, Dinner, Supper},
		},
		RoleMacroShift: map[string]MacroShift{
			"balanced":     {Protein: 1, Carbs: 1, Fat: 1},
			"pre_workout":  {Protein: 0.85, Carbs: 1.35, Fat: 0.50},
			"post_workout": {Protein: 1.30, Carbs: 1.15, Fat: 0.50},
			"light":        {Protein: 1.10, Carbs: 0.85, Fat: 0.95},
		},
		WorkoutMeals: map[string]WorkoutMeals{
			"morning": {Pre: Breakfast, Post: Lunch},
			"midday":  {Pre: Lunch, Post: Snack},
			"evening": {Pre: Snack, Post: Dinner},
		},
		KcalPerKg: 7700,
		Assessment: Assessment{
			MinLoggedDays: 10, CalorieTolerance: 0.10, ProteinTolerance: 0.15,
			RecalibrationThreshold: 0.08, StrongAdherence: 0.85, WeakAdherence: 0.60,
		},
		Adaptation: Adaptation{StepKcal: 150, MaxStepRatio: 0.10},
		CycleWeeks: 4,
	}
}

// LoadConfig lê uma configuração e valida-a.
//
// A validação não é cerimónia: uma linha de `mealSplit` que não some 1 reparte
// calorias que não existem, e o erro só aparece no prato de alguém.
func LoadConfig(r io.Reader) (Config, error) {
	var c Config
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return c, fmt.Errorf("configuração de nutrição ilegível: %w", err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) Validate() error {
	if c.MinDailyCalories <= 0 {
		return fmt.Errorf("minDailyCalories tem de ser positivo, é %d", c.MinDailyCalories)
	}
	if c.MinFatRatio <= 0 || c.MinFatRatio >= 1 {
		return fmt.Errorf("minFatRatio fora de ]0,1[: %v", c.MinFatRatio)
	}
	for n, split := range c.MealSplit {
		var sum float64
		for _, r := range split {
			sum += r
		}
		// Tolerância de meio milésimo: as fracções são escritas à mão e
		// 0.22+0.30+0.13+0.25+0.10 não é exactamente 1 em vírgula flutuante.
		if sum < 0.9995 || sum > 1.0005 {
			return fmt.Errorf("mealSplit[%d] soma %.4f, tem de somar 1", n, sum)
		}
		if len(split) != n {
			return fmt.Errorf("mealSplit[%d] tem %d fracções", n, len(split))
		}
	}
	for n, slots := range c.SlotsByCount {
		if len(slots) != n {
			return fmt.Errorf("slotsByCount[%d] tem %d lugares", n, len(slots))
		}
	}
	return nil
}
