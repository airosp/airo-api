package training

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Paridade com o TypeScript: os 128 subconjuntos possíveis de dias de treino ×
// os 7 dias da semana. Exaustivo, porque o espaço cabe todo — não há amostra a
// escolher nem caso de canto por esquecer.
//
// Gerado a correr `planLabelOn` de `mobile/modules/workout-engine/plan-label.ts`.
func TestParidadePlanLabel(t *testing.T) {
	raw, err := os.ReadFile("testdata/ts-plan-label.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var data struct {
		Cases []struct {
			Days    []int  `json:"days"`
			Weekday int    `json:"weekday"`
			Label   string `json:"label"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}
	if len(data.Cases) != 896 {
		t.Fatalf("esperava 896 casos, tenho %d", len(data.Cases))
	}

	// 2026-09-07 é uma segunda-feira: somar o índice do dia dá o dia da semana
	// que o caso descreve.
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)

	for _, c := range data.Cases {
		day := monday.AddDate(0, 0, c.Weekday)
		if got := PlanLabelOn(c.Days, day); got != c.Label {
			t.Errorf("dias=%v dia=%d (%s): Go=%q TS=%q",
				c.Days, c.Weekday, day.Weekday(), got, c.Label)
		}
	}
}

// A segunda-feira é o dia 0 em todo o lado — no plano, no perfil e aqui.
// Enganar-se nisto desloca a semana inteira em um dia, e o erro só aparece a
// quem treina em dias salteados.
func TestPlanLabelSegundaEDiaZero(t *testing.T) {
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	if monday.Weekday() != time.Monday {
		t.Fatalf("a data de referência não é segunda")
	}
	if got := PlanLabelOn([]int{0}, monday); got != "Upper Body" {
		t.Errorf("segunda com treino: %q", got)
	}
	sunday := monday.AddDate(0, 0, 6)
	if got := PlanLabelOn([]int{6}, sunday); got != "Upper Body" {
		t.Errorf("domingo com treino: %q", got)
	}
	if got := PlanLabelOn([]int{0}, sunday); got != RecoveryLabel {
		t.Errorf("domingo sem treino: %q", got)
	}
}
