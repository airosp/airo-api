// Package journey é o Journey Engine: horizonte, fases, adesão, risco e
// adaptação. Puro — recebe um instantâneo, devolve uma decisão.
package journey

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// ⚠️ Como os do Goal Engine, estes limiares carecem de validação por
// profissionais de exercício antes de qualquer lançamento. São dados, não
// constantes compiladas.

type Intensity string

const (
	IntensityLow      Intensity = "low"
	IntensityModerate Intensity = "moderate"
	IntensityHigh     Intensity = "high"
)

type Progression string

const (
	ProgressionHold     Progression = "hold"
	ProgressionLinear   Progression = "linear"
	ProgressionAdaptive Progression = "adaptive"
)

type Recovery string

const (
	RecoveryStandard Recovery = "standard"
	RecoveryExtra    Recovery = "extra"
	RecoveryDeload   Recovery = "deload"
)

type PhaseKind string

const (
	Adaptation       PhaseKind = "adaptation"
	Development      PhaseKind = "development"
	ProgressionPhase PhaseKind = "progression"
	Transition       PhaseKind = "transition"
	Maintenance      PhaseKind = "maintenance"
)

type PhasePlan struct {
	Intensity      Intensity   `json:"intensity"`
	FrequencyDelta int         `json:"frequencyDelta"`
	DurationFactor float64     `json:"durationFactor"`
	Progression    Progression `json:"progression"`
	Recovery       Recovery    `json:"recovery"`
}

type Config struct {
	Version string `json:"version"`

	// PhaseSplit reparte uma jornada com prazo fechado, em fracção da duração.
	PhaseSplit struct {
		Adaptation  float64 `json:"adaptation"`
		Development float64 `json:"development"`
		Progression float64 `json:"progression"`
		Transition  float64 `json:"transition"`
	} `json:"phaseSplit"`

	// AdaptationWeeks: uma fase de adaptação nunca é mais curta nem mais longa
	// do que isto, seja qual for a fracção.
	AdaptationWeeks struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"adaptationWeeks"`

	// OpenEndedCycleWeeks: jornadas sem prazo correm em ciclos. A jornada é a
	// mesma; o ciclo é que repete.
	OpenEndedCycleWeeks int `json:"openEndedCycleWeeks"`

	PhasePlan map[PhaseKind]PhasePlan `json:"phasePlan"`

	// AdherenceWeights — a adesão **não** é `concluídas / planeadas`.
	AdherenceWeights struct {
		Frequency  float64 `json:"frequency"`
		Duration   float64 `json:"duration"`
		Completion float64 `json:"completion"`
		Schedule   float64 `json:"schedule"`
	} `json:"adherenceWeights"`

	Trend struct {
		WindowDays int `json:"windowDays"`
		MinSamples int `json:"minSamples"`
	} `json:"trend"`

	Risk struct {
		LowAdherence struct {
			Medium float64 `json:"medium"`
			High   float64 `json:"high"`
		} `json:"lowAdherence"`
		RapidChangeRatio        float64 `json:"rapidChangeRatio"`
		PlateauWeeks            int     `json:"plateauWeeks"`
		PlateauRateKg           float64 `json:"plateauRateKg"`
		VolumeSpikeRatio        float64 `json:"volumeSpikeRatio"`
		VolumeSpikeFloorMinutes int     `json:"volumeSpikeFloorMinutes"`
		StalledStartWeeks       int     `json:"stalledStartWeeks"`
	} `json:"risk"`

	Adaptation struct {
		StrongAdherence float64 `json:"strongAdherence"`
		WeakAdherence   float64 `json:"weakAdherence"`
	} `json:"adaptation"`
}

func DefaultConfig() Config {
	var c Config
	c.Version = "2026-09-12"
	c.PhaseSplit.Adaptation = 0.15
	c.PhaseSplit.Development = 0.40
	c.PhaseSplit.Progression = 0.35
	c.PhaseSplit.Transition = 0.10
	c.AdaptationWeeks.Min, c.AdaptationWeeks.Max = 1, 3
	c.OpenEndedCycleWeeks = 4
	c.PhasePlan = map[PhaseKind]PhasePlan{
		Adaptation:       {IntensityLow, -1, 0.85, ProgressionHold, RecoveryExtra},
		Development:      {IntensityModerate, 0, 1.00, ProgressionLinear, RecoveryStandard},
		ProgressionPhase: {IntensityHigh, 0, 1.10, ProgressionAdaptive, RecoveryStandard},
		Transition:       {IntensityModerate, -1, 0.90, ProgressionHold, RecoveryExtra},
		Maintenance:      {IntensityModerate, 0, 1.00, ProgressionHold, RecoveryStandard},
	}
	c.AdherenceWeights.Frequency = 0.40
	c.AdherenceWeights.Duration = 0.20
	c.AdherenceWeights.Completion = 0.25
	c.AdherenceWeights.Schedule = 0.15
	c.Trend.WindowDays, c.Trend.MinSamples = 7, 2
	c.Risk.LowAdherence.Medium, c.Risk.LowAdherence.High = 0.70, 0.50
	c.Risk.RapidChangeRatio = 0.015
	c.Risk.PlateauWeeks, c.Risk.PlateauRateKg = 3, 0.1
	c.Risk.VolumeSpikeRatio = 0.40
	// Abaixo deste volume de base, retomar não é excesso de carga — sem o piso,
	// quem volta de 30 para 45 minutos era alertado.
	c.Risk.VolumeSpikeFloorMinutes = 60
	c.Risk.StalledStartWeeks = 2
	c.Adaptation.StrongAdherence, c.Adaptation.WeakAdherence = 0.85, 0.60
	return c
}

func LoadConfig(r io.Reader) (Config, error) {
	var c Config
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return c, fmt.Errorf("configuração da jornada ilegível: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	p := c.PhaseSplit
	if sum := p.Adaptation + p.Development + p.Progression + p.Transition; math.Abs(sum-1) > 0.0005 {
		return fmt.Errorf("as fracções das fases somam %.4f, têm de somar 1", sum)
	}
	w := c.AdherenceWeights
	if sum := w.Frequency + w.Duration + w.Completion + w.Schedule; math.Abs(sum-1) > 0.0005 {
		return fmt.Errorf("os pesos da adesão somam %.4f, têm de somar 1", sum)
	}
	if c.AdaptationWeeks.Min > c.AdaptationWeeks.Max || c.AdaptationWeeks.Min < 1 {
		return fmt.Errorf("intervalo de adaptação inválido")
	}
	if c.OpenEndedCycleWeeks < 1 {
		return fmt.Errorf("ciclo de horizonte aberto tem de ter pelo menos uma semana")
	}
	if c.Adaptation.WeakAdherence >= c.Adaptation.StrongAdherence {
		return fmt.Errorf("adesão fraca tem de ser menor do que a forte")
	}
	return nil
}
