package training

import (
	"encoding/json"
	"fmt"
	"io"
)

// ⚠️ Isto é prescrição de exercício, não estética.
//
// Os valores seguem práticas comuns (3 séries, 8–12 repetições, 60–120 s de
// descanso em compostos), mas **merecem revisão de um profissional** antes de
// valerem como recomendação — sobretudo os descansos e o volume para
// iniciantes. São dados, não constantes compiladas.

type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type Hydration struct {
	// EveryExercises: uma pausa a cada N exercícios concluídos.
	EveryExercises int `json:"everyExercises"`
	Seconds        int `json:"seconds"`
	// MinExercisesForBreak: abaixo disto a sessão é curta demais para
	// interromper.
	MinExercisesForBreak int `json:"minExercisesForBreak"`
}

type Config struct {
	Version string `json:"version"`

	SetsByExperience map[Experience]int `json:"setsByExperience"`

	RepsByPattern map[MovementPattern]int `json:"repsByPattern"`
	TimeByPattern map[MovementPattern]int `json:"timeByPattern"`
	// RestByPattern — recuperar de um agachamento não é recuperar de uma
	// prancha, e por isso o descanso é por padrão e não global.
	RestByPattern          map[MovementPattern]int `json:"restByPattern"`
	RestFactorByExperience map[Experience]float64  `json:"restFactorByExperience"`

	TransitionBonusSeconds int `json:"transitionBonusSeconds"`
	GetReadySeconds        int `json:"getReadySeconds"`
	// SecondsPerRep é a estimativa para comparar séries a repetições com
	// séries a tempo.
	SecondsPerRep int `json:"secondsPerRep"`

	Hydration Hydration `json:"hydration"`

	ExerciseCountRange Range `json:"exerciseCountRange"`
	ExtraSetsCap       int   `json:"extraSetsCap"`
	RecoveryMinutes    int   `json:"recoveryMinutes"`

	KcalPerMinute map[Focus]float64 `json:"kcalPerMinute"`

	PatternsByFocus  map[Focus][]MovementPattern `json:"patternsByFocus"`
	FocusTitles      map[Focus]string            `json:"focusTitles"`
	FocusByPlanLabel map[string]Focus            `json:"focusByPlanLabel"`
	RecoveryLabels   []string                    `json:"recoveryLabels"`
}

func DefaultConfig() Config {
	recoveryLabels := []string{"Recovery", "Descanso"}
	focusByLabel := map[string]Focus{
		"Upper Body": FocusUpper,
		"Lower Body": FocusLower,
		"Cardio":     FocusCardio,
		"Full Body":  FocusFull,
		"Mobility":   FocusMobility,
	}
	// Os dias de recuperação saem da mesma lista que lhes dá os minutos, em vez
	// de serem escritos outra vez. Mantidas à mão, as duas listas divergiram.
	for _, l := range recoveryLabels {
		focusByLabel[l] = FocusMobility
	}

	return Config{
		Version:          "2026-09-12",
		SetsByExperience: map[Experience]int{Beginner: 2, Intermediate: 3, Advanced: 4},
		RepsByPattern: map[MovementPattern]int{
			Push: 10, Pull: 10, Squat: 12, Hinge: 10, Lunge: 10, Core: 12, Cardio: 12, Mobility: 10,
		},
		TimeByPattern: map[MovementPattern]int{
			Push: 30, Pull: 30, Squat: 40, Hinge: 40, Lunge: 40, Core: 40, Cardio: 60, Mobility: 45,
		},
		RestByPattern: map[MovementPattern]int{
			Push: 75, Pull: 75, Squat: 90, Hinge: 90, Lunge: 75, Core: 45, Cardio: 60, Mobility: 30,
		},
		RestFactorByExperience: map[Experience]float64{Beginner: 1.2, Intermediate: 1, Advanced: 0.85},
		TransitionBonusSeconds: 20,
		GetReadySeconds:        12,
		SecondsPerRep:          3,
		Hydration:              Hydration{EveryExercises: 3, Seconds: 40, MinExercisesForBreak: 4},
		ExerciseCountRange:     Range{Min: 3, Max: 10},
		ExtraSetsCap:           2,
		RecoveryMinutes:        20,
		KcalPerMinute: map[Focus]float64{
			FocusUpper: 7, FocusLower: 8, FocusCardio: 10, FocusFull: 8, FocusMobility: 4,
		},
		PatternsByFocus: map[Focus][]MovementPattern{
			FocusUpper:    {Push, Pull, Push, Pull, Core},
			FocusLower:    {Squat, Hinge, Lunge, Squat, Core},
			FocusCardio:   {Cardio, Cardio, Core, Cardio},
			FocusFull:     {Squat, Push, Hinge, Pull, Core},
			FocusMobility: {Mobility, Mobility, Core, Mobility},
		},
		FocusTitles: map[Focus]string{
			FocusUpper: "Tronco", FocusLower: "Pernas", FocusCardio: "Cardio",
			FocusFull: "Corpo inteiro", FocusMobility: "Mobilidade",
		},
		FocusByPlanLabel: focusByLabel,
		RecoveryLabels:   recoveryLabels,
	}
}

// IsRecoveryDay diz se este rótulo do plano é um dia de recuperação.
func (c Config) IsRecoveryDay(planLabel string) bool {
	for _, l := range c.RecoveryLabels {
		if l == planLabel {
			return true
		}
	}
	return false
}

// PlanDayMinutes — quantos minutos vale este dia do plano.
//
// Um sítio só: o subtítulo do plano, o cartão da home e o Modo Foco leem daqui,
// e por isso não podem prometer durações diferentes do mesmo dia.
func (c Config) PlanDayMinutes(planLabel string, workoutMinutes int) int {
	if c.IsRecoveryDay(planLabel) {
		return c.RecoveryMinutes
	}
	return workoutMinutes
}

func LoadConfig(r io.Reader) (Config, error) {
	var c Config
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return c, fmt.Errorf("configuração de treino ilegível: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.ExerciseCountRange.Min < 1 || c.ExerciseCountRange.Min > c.ExerciseCountRange.Max {
		return fmt.Errorf("intervalo de exercícios inválido: %+v", c.ExerciseCountRange)
	}
	if c.SecondsPerRep <= 0 {
		return fmt.Errorf("secondsPerRep tem de ser positivo")
	}
	for f, patterns := range c.PatternsByFocus {
		if len(patterns) == 0 {
			return fmt.Errorf("foco %q sem padrões", f)
		}
	}
	for f := range c.PatternsByFocus {
		if _, ok := c.FocusTitles[f]; !ok {
			return fmt.Errorf("foco %q sem título", f)
		}
	}
	return nil
}
