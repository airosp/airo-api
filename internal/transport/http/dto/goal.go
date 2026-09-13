// Package dto são os tipos do contrato HTTP.
//
// ⚠️ Nunca se expõe um tipo de domínio directamente. Um campo acrescentado a
// uma estrutura interna passaria a sair na API sem ninguém decidir que devia —
// e o score do Goal Engine é exactamente o tipo de coisa que nunca pode sair.
package dto

import "github.com/airosp/airo-api/internal/engine/goal"

type CreateGoalRequest struct {
	Type      string `json:"type"`
	Horizon   string `json:"horizon"`
	Direction string `json:"direction"`
	Priority  string `json:"priority"`
	StartDate string `json:"startDate"`
	// TargetDate tem de ser null fora do horizonte fechado.
	TargetDate *string `json:"targetDate"`

	Targets []TargetRequest `json:"targets"`
}

type TargetRequest struct {
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Direction string  `json:"direction"`
}

type Signal struct {
	Category string `json:"category"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type Milestone struct {
	Value    float64 `json:"value"`
	Progress float64 `json:"progress"`
	IsTarget bool    `json:"isTarget"`
}

type Recommendation struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Priority string `json:"priority"`
	Action   any    `json:"action"`
}

// Assessment é o que a app precisa para escolher o tom.
//
// ⚠️ **Sem o score.** `goalEngineConfig.scoring` diz: "Nunca são mostrados ao
// utilizador". Pô-lo aqui seria convidar a interface a desenhá-lo.
type Assessment struct {
	Status          string           `json:"status"`
	Signals         []Signal         `json:"signals"`
	Milestones      []Milestone      `json:"milestones"`
	Recommendations []Recommendation `json:"recommendations"`
}

type Phase struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Intent    string `json:"intent"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Weeks     int    `json:"weeks"`
}

type Journey struct {
	ID         string  `json:"id"`
	StartDate  string  `json:"startDate"`
	TargetDate *string `json:"targetDate"`
	Phases     []Phase `json:"phases,omitempty"`
	CycleWeeks *int    `json:"cycleWeeks,omitempty"`
}

type Goal struct {
	ID      string `json:"id"`
	Horizon string `json:"horizon"`
	Status  string `json:"status"`
}

type NutritionStrategy struct {
	CalorieTarget int `json:"calorieTarget"`
	Macros        struct {
		Protein int `json:"protein"`
		Carbs   int `json:"carbs"`
		Fat     int `json:"fat"`
	} `json:"macros"`
}

type Plan struct {
	ID             string `json:"id"`
	Frequency      int    `json:"frequency"`
	SessionMinutes int    `json:"sessionMinutes"`
	Intensity      string `json:"intensity"`
}

type CreateGoalResponse struct {
	Goal       Goal              `json:"goal"`
	Journey    Journey           `json:"journey"`
	Plan       Plan              `json:"plan"`
	Nutrition  NutritionStrategy `json:"nutrition"`
	Assessment Assessment        `json:"assessment"`
}

// FromAssessment traduz a avaliação do motor para o contrato.
//
// A tradução é deliberada e não automática: é aqui que o score fica de fora, e
// um `json:"-"` no domínio não chegaria — alguém acabaria por o tirar.
func FromAssessment(a goal.Assessment) Assessment {
	out := Assessment{Status: string(a.Status)}
	for _, s := range a.Signals {
		out.Signals = append(out.Signals, Signal{
			Category: string(s.Category), Severity: string(s.Severity), Message: s.Message,
		})
	}
	for _, m := range a.Milestones {
		out.Milestones = append(out.Milestones, Milestone{
			Value: m.WeightKg, Progress: m.Progress, IsTarget: m.IsTarget,
		})
	}
	for _, r := range a.Recommendations {
		out.Recommendations = append(out.Recommendations, Recommendation{
			ID: r.ID, Label: r.Label, Priority: r.Priority, Action: r.Action,
		})
	}
	return out
}

// ActiveGoalResponse é o objetivo em vigor, como a app o desenha.
//
// ⚠️ **Sem o score, aqui também.** O mesmo que vale para a avaliação vale para
// a leitura: `goalEngineConfig.scoring` diz que nunca é mostrado, e o sítio
// mais fácil de o deixar escapar é numa resposta que "só devolve o que está
// gravado".
type ActiveGoalResponse struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Horizon   string `json:"horizon"`
	Direction string `json:"direction"`
	Priority  string `json:"priority"`
	Status    string `json:"status"`

	Journey ActiveJourney `json:"journey"`
	Targets []TargetView  `json:"targets"`
}

type ActiveJourney struct {
	ID         string  `json:"id"`
	Horizon    string  `json:"horizon"`
	StartDate  string  `json:"startDate"`
	TargetDate *string `json:"targetDate"`
	CycleWeeks *int    `json:"cycleWeeks,omitempty"`
	Status     string  `json:"status"`
}

type TargetView struct {
	Metric    string  `json:"metric"`
	Direction string  `json:"direction"`
	Baseline  float64 `json:"baseline"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	DueDate   *string `json:"dueDate"`
}
