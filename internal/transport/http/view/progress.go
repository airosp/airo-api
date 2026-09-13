package view

import (
	"strconv"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/engine/journey"
)

// ── O retrato do progresso ───────────────────────────────────────────────────
//
// ⚠️ **Duas formas, conforme o horizonte.** Não é um detalhe de serialização: é
// a diferença entre "estás a X semanas do teu peso" e "cumpriste 11 de 12
// treinos". Devolver `forecast: null` num horizonte aberto convidava a interface
// a mostrar um espaço vazio onde devia estar consistência.

type ProgressSnapshot struct {
	Horizon string `json:"horizon"`

	// Horizonte fechado.
	Trend     *TrendView     `json:"trend,omitempty"`
	Adherence *AdherenceView `json:"adherence,omitempty"`
	Forecast  *ForecastView  `json:"forecast,omitempty"`

	// Horizonte aberto.
	Cycle       *CycleViewJSON   `json:"cycle,omitempty"`
	Consistency *ConsistencyView `json:"consistency,omitempty"`
	Progression *ProgressionView `json:"progression,omitempty"`

	Risks []RiskView `json:"risks"`

	// Paused muda o que o ecrã diz. Uma adesão baixa porque alguém avisou que
	// ia estar fora não é uma adesão baixa — e mostrá-las igual culpa quem
	// avisou.
	Paused *PausedView `json:"paused,omitempty"`
	// JourneyID é o que os botões de pausa e retoma precisam.
	JourneyID string `json:"journeyId,omitempty"`
}

type PausedView struct {
	Since string `json:"since"`
	Days  int    `json:"days"`
	Label string `json:"label"`
}

type TrendView struct {
	Metric         string   `json:"metric"`
	RollingAverage float64  `json:"rollingAverage"`
	RatePerWeek    *float64 `json:"ratePerWeek"`
	Samples        int      `json:"samples"`
	Confidence     string   `json:"confidence"`
	// Headline é a tendência por palavras. O cliente mostra-a; não a compõe a
	// partir do sinal de um número.
	Headline string `json:"headline"`
}

type AdherenceView struct {
	Overall    float64 `json:"overall"`
	Frequency  float64 `json:"frequency"`
	Duration   float64 `json:"duration"`
	Completion float64 `json:"completion"`
	Schedule   float64 `json:"schedule"`
	// Evaluable falso quer dizer "ainda não há nada a julgar". Sem isto, quem
	// começou ontem via 0% e concluía que já estava a falhar.
	Evaluable bool `json:"evaluable"`
}

type ForecastView struct {
	ExpectedOn *string `json:"expectedOn"`
	Confidence string  `json:"confidence"`
	Headline   string  `json:"headline"`
}

type CycleViewJSON struct {
	Index      int    `json:"index"`
	ReviewDate string `json:"reviewDate"`
}

type ConsistencyView struct {
	SessionsPlanned int     `json:"sessionsPlanned"`
	SessionsDone    int     `json:"sessionsDone"`
	Rate            float64 `json:"rate"`
	Headline        string  `json:"headline"`
}

type ProgressionView struct {
	VolumeTrend string `json:"volumeTrend"`
	Detail      string `json:"detail"`
}

type RiskView struct {
	Type  string `json:"type"`
	Level string `json:"level"`
	// Detail é a primeira evidência, que é a que cabe num cartão.
	Detail         string   `json:"detail"`
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
	Confidence     string   `json:"confidence"`
}

// SnapshotData é o que a vista precisa para desenhar, e nada mais.
//
// Tipos do motor e campos simples — nunca o resultado do serviço. O `view` é
// importado por ele, e depender de volta fechava um ciclo que o compilador
// recusa. A fronteira aqui não é estilo: é o que mantém a apresentação
// substituível sem mexer em quem decide.
type SnapshotData struct {
	Horizon   string
	MetricKey string

	JourneyID string
	// Paused muda o que o ecrã diz — ver `PausedView`.
	Paused      bool
	PausedSince *time.Time
	PausedDays  int

	Trend     *journey.Trend
	Adherence journey.Adherence
	Risks     []journey.Risk

	// Horizonte fechado.
	ForecastOn         *time.Time
	ForecastConfidence string
	TemPrevisao        bool

	// Horizonte aberto.
	CycleIndex      int
	CycleReview     *time.Time
	SessionsPlanned int
	SessionsDone    int
	ConsistencyRate float64
	TemConsistencia bool
	VolumeTrend     string
}

func BuildProgressSnapshot(s SnapshotData) ProgressSnapshot {
	out := ProgressSnapshot{Horizon: s.Horizon, Risks: []RiskView{}, JourneyID: s.JourneyID}
	if s.Paused && s.PausedSince != nil {
		out.Paused = &PausedView{
			Since: s.PausedSince.Format("2006-01-02"),
			Days:  s.PausedDays,
			Label: "Em pausa desde " + diaPorExtenso(*s.PausedSince) + ".",
		}
	}

	if s.Trend != nil {
		out.Trend = &TrendView{
			Metric:         s.MetricKey,
			RollingAverage: s.Trend.RollingAverage,
			RatePerWeek:    s.Trend.RatePerWeek,
			Samples:        s.Trend.Samples,
			Confidence:     string(s.Trend.Confidence),
			Headline:       tendenciaPorPalavras(s.Trend),
		}
	}

	a := s.Adherence
	out.Adherence = &AdherenceView{
		Overall: a.Score, Frequency: a.Frequency, Duration: a.Duration,
		Completion: a.Completion, Schedule: a.Schedule, Evaluable: a.Evaluable,
	}

	if s.TemPrevisao {
		f := &ForecastView{Confidence: s.ForecastConfidence}
		if s.ForecastOn != nil {
			d := s.ForecastOn.Format("2006-01-02")
			f.ExpectedOn = &d
			f.Headline = "Ao ritmo actual, por volta de " + diaPorExtenso(*s.ForecastOn) + "."
		} else {
			// Sem data é informação, não ausência dela: o ritmo actual não leva
			// ao alvo, e dizê-lo é mais útil do que um espaço em branco.
			f.Headline = "Ao ritmo actual não há data à vista."
		}
		out.Forecast = f
	}

	if s.CycleReview != nil {
		out.Cycle = &CycleViewJSON{
			Index: s.CycleIndex, ReviewDate: s.CycleReview.Format("2006-01-02"),
		}
	}
	if s.TemConsistencia {
		out.Consistency = &ConsistencyView{
			SessionsPlanned: s.SessionsPlanned, SessionsDone: s.SessionsDone, Rate: s.ConsistencyRate,
			Headline: consistenciaPorPalavras(s.SessionsDone, s.SessionsPlanned),
		}
	}
	if s.VolumeTrend != "" {
		out.Progression = &ProgressionView{VolumeTrend: s.VolumeTrend, Detail: volumePorPalavras(s.VolumeTrend)}
	}

	for _, r := range s.Risks {
		v := RiskView{
			Type: string(r.Type), Level: string(r.Level),
			Evidence: r.Evidence, Recommendation: r.Recommendation,
			Confidence: string(r.Confidence),
		}
		if len(r.Evidence) > 0 {
			v.Detail = r.Evidence[0]
		}
		out.Risks = append(out.Risks, v)
	}
	return out
}

// tendenciaPorPalavras — o sinal de um número não é uma frase.
func tendenciaPorPalavras(t *journey.Trend) string {
	if t.RatePerWeek == nil {
		return "Ainda sem ritmo definido — faltam pesagens."
	}
	r := *t.RatePerWeek
	switch {
	case r <= -0.05:
		return "A descer " + kg(-r) + " por semana."
	case r >= 0.05:
		return "A subir " + kg(r) + " por semana."
	default:
		return "Estável nas últimas semanas."
	}
}

func consistenciaPorPalavras(feitos, planeados int) string {
	if planeados == 0 {
		return "Ainda não há semanas completas para contar."
	}
	return plural(feitos) + " de " + plural(planeados) + " treinos."
}

func volumePorPalavras(trend string) string {
	switch trend {
	case "up":
		return "Mais volume do que nas semanas anteriores."
	case "down":
		return "Menos volume do que nas semanas anteriores."
	default:
		return "Volume estável."
	}
}

func kg(v float64) string {
	return decimal(v, 2) + " kg"
}

func diaPorExtenso(t time.Time) string {
	meses := []string{"janeiro", "fevereiro", "março", "abril", "maio", "junho",
		"julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}
	return plural(t.Day()) + " de " + meses[int(t.Month())-1] + " de " + plural(t.Year())
}

// plural formata um inteiro. Existe para não haver `fmt.Sprintf("%d")` espalhado
// pelas frases — e para o dia em que alguém quiser separador de milhares.
func plural(n int) string {
	return strconv.Itoa(n)
}

// decimal com vírgula, que é como se escrevem números em português.
func decimal(v float64, casas int) string {
	s := strconv.FormatFloat(v, 'f', casas, 64)
	return strings.Replace(s, ".", ",", 1)
}

// ── Propostas de adaptação ───────────────────────────────────────────────────

type AdaptationView struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Title e Reason são o que a pessoa lê. O título vem daqui e não do cliente:
	// `reduce_load` não é uma frase.
	Title  string `json:"title"`
	Reason string `json:"reason"`
	// Changes descritas por palavras, para o cartão não ter de as interpretar.
	Changes   []string `json:"changes"`
	CreatedAt string   `json:"createdAt"`
}

var titulosDeAdaptacao = map[string]string{
	"reduce_load":     "Aliviar o plano",
	"increase_load":   "Subir a exigência",
	"maintain":        "Manter como está",
	"review_goal":     "Rever o objectivo",
	"change_strategy": "Mudar de abordagem",
}

func BuildAdaptations(rows []AdaptationRowLike) []AdaptationView {
	out := make([]AdaptationView, 0, len(rows))
	for _, r := range rows {
		titulo, ok := titulosDeAdaptacao[r.Kind]
		if !ok {
			titulo = "Ajuste ao plano"
		}
		v := AdaptationView{
			ID: r.ID, Kind: r.Kind, Title: titulo,
			Changes:   []string{},
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
		}
		if razao, ok := r.Payload["reason"].(string); ok {
			v.Reason = razao
		}
		if changes, ok := r.Payload["changes"].(map[string]any); ok {
			if f, ok := changes["frequency"].(float64); ok {
				v.Changes = append(v.Changes, plural(int(f))+" treinos por semana")
			}
			if d, ok := changes["sessionDurationMinutes"].(float64); ok {
				v.Changes = append(v.Changes, plural(int(d))+" minutos por treino")
			}
			if i, ok := changes["intensity"].(string); ok {
				v.Changes = append(v.Changes, "Intensidade "+intensidadePorPalavras(i))
			}
			if rec, ok := changes["recoveryStrategy"].(string); ok && rec == "extra" {
				v.Changes = append(v.Changes, "Mais descanso entre treinos")
			}
		}
		out = append(out, v)
	}
	return out
}

func intensidadePorPalavras(i string) string {
	switch i {
	case "low":
		return "baixa"
	case "high":
		return "alta"
	default:
		return "moderada"
	}
}

// AdaptationRowLike é a forma mínima que a vista precisa — e não o tipo do
// repositório, que o `view` não pode importar sem fechar um ciclo.
type AdaptationRowLike struct {
	ID        string
	Kind      string
	Payload   map[string]any
	CreatedAt time.Time
}
