// Package domain tem os tipos partilhados. Sem dependências: nem HTTP, nem SQL,
// nem motores. É o vocabulário, não o comportamento.
package domain

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
)

// MetricType diz o que uma série mede.
//
// Polimórfico de propósito: nem tudo é repetição. Sem isto não se representa
// musculação com carga (`4 × 10 @ 40 kg`) nem corrida (`5 × 400 m`) — que é
// metade do caso de uso de uma app de treino.
type MetricType string

const (
	MetricReps     MetricType = "reps"
	MetricTime     MetricType = "time"
	MetricDistance MetricType = "distance"
	MetricLoadReps MetricType = "load_reps"
)

// Metric é o alvo de uma série, ou o que nela aconteceu.
//
// Os campos são ponteiros porque **zero e ausente são coisas diferentes**:
// `reps = 0` numa série tentada e falhada é um registo válido, e `nil` quer
// dizer que não se mediu.
type Metric struct {
	Type            MetricType `json:"type"`
	Reps            *int       `json:"reps,omitempty"`
	DurationSeconds *int       `json:"durationSeconds,omitempty"`
	DistanceMeters  *int       `json:"distanceMeters,omitempty"`
	WeightKg        *float64   `json:"weightKg,omitempty"`
}

// secondsPerRep é a estimativa usada para comparar tempo com repetições.
// Vem de workoutConfig.secondsPerRep — ver 03-regras-de-negocio.md §3.
const secondsPerRep = 3.0

// metersPerVolumeUnit converte distância para a mesma escala. 100 m ≈ uma
// unidade de volume, para uma corrida de 5 km não esmagar todo o resto.
const metersPerVolumeUnit = 100.0

// Volume dá um número comparável entre sessões, para a progressão.
//
// Não é uma medida física: é uma escala única que permite dizer se a semana 3
// teve mais trabalho do que a semana 1 quando os exercícios mudaram.
func (m Metric) Volume() float64 {
	switch m.Type {
	case MetricLoadReps:
		if m.Reps != nil && m.WeightKg != nil {
			return float64(*m.Reps) * *m.WeightKg
		}
	case MetricReps:
		if m.Reps != nil {
			return float64(*m.Reps)
		}
	case MetricTime:
		if m.DurationSeconds != nil {
			return float64(*m.DurationSeconds) / secondsPerRep
		}
	case MetricDistance:
		if m.DistanceMeters != nil {
			return float64(*m.DistanceMeters) / metersPerVolumeUnit
		}
	}
	return 0
}

// Validate garante que a métrica tem o campo que o seu tipo exige.
//
// Uma `Metric{Type: "reps"}` sem `Reps` passaria silenciosamente e daria volume
// zero — um treino inteiro a contar como nada, sem nada a dizê-lo.
func (m Metric) Validate() error {
	switch m.Type {
	case MetricReps:
		if m.Reps == nil {
			return errors.New("métrica de repetições sem reps")
		}
	case MetricTime:
		if m.DurationSeconds == nil {
			return errors.New("métrica de tempo sem durationSeconds")
		}
	case MetricDistance:
		if m.DistanceMeters == nil {
			return errors.New("métrica de distância sem distanceMeters")
		}
	case MetricLoadReps:
		if m.Reps == nil || m.WeightKg == nil {
			return errors.New("métrica de carga sem reps ou weightKg")
		}
	default:
		return fmt.Errorf("tipo de métrica desconhecido: %q", m.Type)
	}
	return nil
}

// Value implementa driver.Valuer: a métrica vai para `jsonb` sem serialização
// manual em cada consulta.
func (m Metric) Value() (driver.Value, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implementa sql.Scanner.
func (m *Metric) Scan(src any) error {
	if src == nil {
		*m = Metric{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("Metric.Scan: tipo inesperado %T", src)
	}
	return json.Unmarshal(b, m)
}

// Construtores. Existem para que um `&n` solto não ande espalhado pelo código.

func Reps(n int) Metric { return Metric{Type: MetricReps, Reps: &n} }

func Seconds(n int) Metric { return Metric{Type: MetricTime, DurationSeconds: &n} }

func Meters(n int) Metric { return Metric{Type: MetricDistance, DistanceMeters: &n} }

func LoadReps(n int, kg float64) Metric {
	return Metric{Type: MetricLoadReps, Reps: &n, WeightKg: &kg}
}
