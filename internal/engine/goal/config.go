// Package goal é o Goal Engine: decide se uma meta é plausível e o que a limita.
//
// Puro. Recebe um instantâneo, devolve uma avaliação. Não lê da base de dados,
// não conhece HTTP, e não escreve nada.
package goal

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// ⚠️ TODOS OS LIMIARES DO GOAL ENGINE VIVEM AQUI.
//
// Estão reunidos por um motivo concreto: os valores abaixo decidem quando a app
// diz a alguém que a sua meta merece revisão, e essa não é uma decisão de
// engenharia. Têm de ser revistos e assinados por profissionais de exercício e
// nutrição antes de qualquer lançamento — e o resto do motor não muda quando
// eles mudarem.
//
// São referências comuns da literatura, usadas como ponto de partida para o
// produto funcionar. **Não são prescrição clínica.**

type RateBand struct {
	Comfortable    float64 `json:"comfortable"`
	RecommendedMax float64 `json:"recommendedMax"`
	Aggressive     float64 `json:"aggressive"`
}

type ActivityBand struct {
	// UpTo em minutos semanais. O último tem +Inf.
	UpTo   float64 `json:"upTo"`
	Factor float64 `json:"factor"`
}

type Config struct {
	Version    string `json:"version"`
	WeeklyRate struct {
		Loss RateBand `json:"loss"`
		Gain RateBand `json:"gain"`
	} `json:"weeklyRate"`

	BodyRange struct {
		LowerBmi float64 `json:"lowerBmi"`
		UpperBmi float64 `json:"upperBmi"`
	} `json:"bodyRange"`

	Magnitude struct {
		NotableRatio float64 `json:"notableRatio"`
		LargeRatio   float64 `json:"largeRatio"`
	} `json:"magnitude"`

	Training struct {
		MinWeeklyMinutes struct {
			Maintain    int `json:"maintain"`
			Change      int `json:"change"`
			LargeChange int `json:"largeChange"`
		} `json:"minWeeklyMinutes"`
	} `json:"training"`

	Energy struct {
		KcalPerKgFat                  float64        `json:"kcalPerKgFat"`
		MinDailyCalories              int            `json:"minDailyCalories"`
		ActivityFactorByWeeklyMinutes []ActivityBand `json:"activityFactorByWeeklyMinutes"`
	} `json:"energy"`

	Milestones struct {
		StepKg   float64 `json:"stepKg"`
		MaxCount int     `json:"maxCount"`
	} `json:"milestones"`

	Progress struct {
		OnTrackToleranceKg float64 `json:"onTrackToleranceKg"`
	} `json:"progress"`

	Scoring Scoring `json:"scoring"`
}

type Scoring struct {
	Excellent  int `json:"excellent"`
	Realistic  int `json:"realistic"`
	Ambitious  int `json:"ambitious"`
	Aggressive int `json:"aggressive"`
	Penalty    struct {
		Info     int `json:"info"`
		Warning  int `json:"warning"`
		Critical int `json:"critical"`
	} `json:"penalty"`
	Weights struct {
		Body        float64 `json:"body"`
		Rate        float64 `json:"rate"`
		Training    float64 `json:"training"`
		Consistency float64 `json:"consistency"`
	} `json:"weights"`
}

// DefaultConfig são os valores em vigor no cliente.
func DefaultConfig() Config {
	var c Config
	c.Version = "2026-09-12"
	c.WeeklyRate.Loss = RateBand{Comfortable: 0.005, RecommendedMax: 0.01, Aggressive: 0.015}
	c.WeeklyRate.Gain = RateBand{Comfortable: 0.0025, RecommendedMax: 0.005, Aggressive: 0.0075}
	c.BodyRange.LowerBmi, c.BodyRange.UpperBmi = 18.5, 24.9
	c.Magnitude.NotableRatio, c.Magnitude.LargeRatio = 0.10, 0.20
	c.Training.MinWeeklyMinutes.Maintain = 60
	c.Training.MinWeeklyMinutes.Change = 120
	c.Training.MinWeeklyMinutes.LargeChange = 150
	c.Energy.KcalPerKgFat = 7700
	c.Energy.MinDailyCalories = 1500
	// Por minutos semanais e não por dias: 3 × 20 min não é o mesmo que 3 × 90 min.
	c.Energy.ActivityFactorByWeeklyMinutes = []ActivityBand{
		{UpTo: 60, Factor: 1.20},
		{UpTo: 150, Factor: 1.35},
		{UpTo: 300, Factor: 1.50},
		{UpTo: 450, Factor: 1.65},
		{UpTo: math.Inf(1), Factor: 1.80},
	}
	c.Milestones.StepKg, c.Milestones.MaxCount = 3, 5
	c.Progress.OnTrackToleranceKg = 1
	c.Scoring.Excellent, c.Scoring.Realistic = 85, 70
	c.Scoring.Ambitious, c.Scoring.Aggressive = 55, 35
	c.Scoring.Penalty.Info, c.Scoring.Penalty.Warning, c.Scoring.Penalty.Critical = 5, 25, 60
	c.Scoring.Weights.Body, c.Scoring.Weights.Rate = 0.30, 0.30
	c.Scoring.Weights.Training, c.Scoring.Weights.Consistency = 0.25, 0.15
	return c
}

func LoadConfig(r io.Reader) (Config, error) {
	var c Config
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return c, fmt.Errorf("configuração do goal engine ilegível: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	w := c.Scoring.Weights
	if sum := w.Body + w.Rate + w.Training + w.Consistency; math.Abs(sum-1) > 0.0005 {
		return fmt.Errorf("os pesos do score somam %.4f, têm de somar 1", sum)
	}
	if c.Scoring.Excellent < c.Scoring.Realistic ||
		c.Scoring.Realistic < c.Scoring.Ambitious ||
		c.Scoring.Ambitious < c.Scoring.Aggressive {
		return fmt.Errorf("os cortes do score têm de ser decrescentes")
	}
	if len(c.Energy.ActivityFactorByWeeklyMinutes) == 0 {
		return fmt.Errorf("sem bandas de factor de actividade")
	}
	if c.BodyRange.LowerBmi >= c.BodyRange.UpperBmi {
		return fmt.Errorf("intervalo de IMC inválido")
	}
	return nil
}
