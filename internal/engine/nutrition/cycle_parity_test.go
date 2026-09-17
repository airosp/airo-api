package nutrition

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

/*
 * Paridade do ciclo com o TypeScript: 1 485 avaliações, 18 ciclos, 4 invariantes.
 *
 * ⚠️ O ciclo **só existia em TypeScript**. `BuildStrategy` e `BuildDayPlan`
 * tinham gémeos em Go com paridade provada; a família do ciclo não tinha
 * nenhum, e por isso a única máquina capaz de dizer "o teu alvo está errado"
 * era o telemóvel de quem estivesse a olhar para o ecrã.
 *
 * Gerado a correr `startCycle`, `assessCycle`, `applyAdaptation` e
 * `checkStrategySafety` de `mobile/modules/nutrition-engine/`. O ficheiro fica
 * versionado: assim a comparação corre em CI, e não só na máquina de quem a fez.
 *
 * Cobre os 5 objectivos × 3 alvos calóricos × 4 ajustes de energia × 3 gastos
 * estimados × 4 durações × 3 desvios de ingestão × 2 níveis de proteína × 5
 * variações de peso — e os seis tipos de adaptação, incluindo o
 * `increase_deficit`, que é o que acontece a quem cumpre o défice e mesmo assim
 * ganha peso.
 */

type casoDeCiclo struct {
	Start []struct {
		In struct {
			StrategyID   string `json:"strategyId"`
			Index        int    `json:"index"`
			StartDateISO string `json:"startDateISO"`
			JourneyID    string `json:"journeyId"`
		} `json:"in"`
		Out Cycle `json:"out"`
	} `json:"start"`

	Assess []struct {
		In struct {
			GoalType         GoalType `json:"goalType"`
			CalorieTarget    int      `json:"calorieTarget"`
			EnergyAdjustment int      `json:"energyAdjustment"`
			BasisTDEE        int      `json:"basisTdee"`
			Macros           Macros   `json:"macros"`
			MealsPerDay      int      `json:"mealsPerDay"`
			BodyWeightKg     float64  `json:"bodyWeightKg"`
			Dias             int      `json:"dias"`
			Desvio           float64  `json:"desvio"`
			FracProt         float64  `json:"fracProt"`
			WeightChangeKg   *float64 `json:"weightChangeKg"`
			PeriodStartISO   string   `json:"periodStartISO"`
			NowISO           string   `json:"nowISO"`
		} `json:"in"`
		Assessment struct {
			ObservedTDEE *int            `json:"observedTdee"`
			Response     Response        `json:"response"`
			Headline     string          `json:"headline"`
			Confidence   Confidence      `json:"confidence"`
			Signals      []Signal        `json:"signals"`
			Adherence    AdherenceResult `json:"adherence"`
		} `json:"assessment"`
		Adaptation struct {
			Kind         string             `json:"kind"`
			Reason       string             `json:"reason"`
			CalorieDelta int                `json:"calorieDelta"`
			MacroChanges map[string]float64 `json:"macroChanges"`
			Applied      bool               `json:"applied"`
		} `json:"adaptation"`
		Applied struct {
			CalorieTarget int      `json:"calorieTarget"`
			Macros        Macros   `json:"macros"`
			BasisTDEE     int      `json:"basisTdee"`
			BasisSource   string   `json:"basisSource"`
			Safety        []Signal `json:"safety"`
		} `json:"applied"`
	} `json:"assess"`

	Safety []struct {
		In struct {
			CalorieTarget int     `json:"calorieTarget"`
			Macros        Macros  `json:"macros"`
			BodyWeightKg  float64 `json:"bodyWeightKg"`
		} `json:"in"`
		Out []Signal `json:"out"`
	} `json:"safety"`
}

func lerCasosDeCiclo(t *testing.T) casoDeCiclo {
	t.Helper()
	bruto, err := os.ReadFile("testdata/ts-cycle.json")
	if err != nil {
		t.Fatalf("casos: %v", err)
	}
	var d casoDeCiclo
	if err := json.Unmarshal(bruto, &d); err != nil {
		t.Fatalf("casos ilegíveis: %v", err)
	}
	return d
}

func TestParidadeStartCycle(t *testing.T) {
	c := DefaultConfig()
	for _, caso := range lerCasosDeCiclo(t).Start {
		got, err := StartCycle(c, StartCycleInput{
			StrategyID:   caso.In.StrategyID,
			Index:        caso.In.Index,
			StartDateISO: caso.In.StartDateISO,
			JourneyID:    caso.In.JourneyID,
		})
		if err != nil {
			t.Fatalf("%s: %v", caso.In.StartDateISO, err)
		}
		if !reflect.DeepEqual(got, caso.Out) {
			t.Errorf("startCycle(%d, %q):\n  Go: %+v\n  TS: %+v",
				caso.In.Index, caso.In.StartDateISO, got, caso.Out)
		}
	}
}

func TestParidadeAssessCycle(t *testing.T) {
	c := DefaultConfig()
	casos := lerCasosDeCiclo(t).Assess
	if len(casos) == 0 {
		t.Fatal("sem casos")
	}

	falhas := 0
	for i, caso := range casos {
		in := caso.In
		estrategia := Strategy2{
			GoalType: in.GoalType, CalorieTarget: in.CalorieTarget, Macros: in.Macros,
			MealsPerDay: in.MealsPerDay, EnergyAdjustment: in.EnergyAdjustment,
			BasisTDEE: in.BasisTDEE, BasisSource: "estimated",
		}
		avaliacao, adaptacao := AssessCycle(c, AssessCycleInput{
			Strategy:       estrategia,
			Logs:           registosDeEnsaio(in.Dias, in.PeriodStartISO, float64(in.CalorieTarget)*in.Desvio, float64(in.Macros.Protein)*in.FracProt),
			PeriodStartISO: in.PeriodStartISO,
			NowISO:         in.NowISO,
			WeightChangeKg: in.WeightChangeKg,
		})

		// A avaliação: o que ela conclui, e porquê.
		if avaliacao.Response != caso.Assessment.Response ||
			avaliacao.Confidence != caso.Assessment.Confidence ||
			avaliacao.Headline != caso.Assessment.Headline ||
			!mesmoInteiro(avaliacao.ObservedTDEE, caso.Assessment.ObservedTDEE) ||
			!reflect.DeepEqual(avaliacao.Signals, naoNilSinais(caso.Assessment.Signals)) {
			falhas++
			if falhas <= 5 {
				t.Errorf("caso %d — avaliação\n  Go: %s/%s/%q obs=%v sinais=%v\n  TS: %s/%s/%q obs=%v sinais=%v",
					i, avaliacao.Response, avaliacao.Confidence, avaliacao.Headline, mostrar(avaliacao.ObservedTDEE), avaliacao.Signals,
					caso.Assessment.Response, caso.Assessment.Confidence, caso.Assessment.Headline, mostrar(caso.Assessment.ObservedTDEE), caso.Assessment.Signals)
			}
			continue
		}

		// A adesão que a sustenta: um número diferente aqui muda tudo o resto.
		if !reflect.DeepEqual(avaliacao.Adherence, caso.Assessment.Adherence) {
			falhas++
			if falhas <= 5 {
				t.Errorf("caso %d — adesão\n  Go: %+v\n  TS: %+v", i, avaliacao.Adherence, caso.Assessment.Adherence)
			}
			continue
		}

		// A proposta.
		if adaptacao.Kind != caso.Adaptation.Kind ||
			adaptacao.Reason != caso.Adaptation.Reason ||
			adaptacao.CalorieDelta != caso.Adaptation.CalorieDelta ||
			adaptacao.Applied != caso.Adaptation.Applied ||
			!reflect.DeepEqual(adaptacao.MacroChanges, naoNilMacros(caso.Adaptation.MacroChanges)) {
			falhas++
			if falhas <= 5 {
				t.Errorf("caso %d — adaptação\n  Go: %s Δ%d aplicada=%v %v %q\n  TS: %s Δ%d aplicada=%v %v %q",
					i, adaptacao.Kind, adaptacao.CalorieDelta, adaptacao.Applied, adaptacao.MacroChanges, adaptacao.Reason,
					caso.Adaptation.Kind, caso.Adaptation.CalorieDelta, caso.Adaptation.Applied, caso.Adaptation.MacroChanges, caso.Adaptation.Reason)
			}
			continue
		}

		// E o que sai de a aplicar.
		proxima, seguranca := ApplyAdaptation(c, ApplyAdaptationInput{
			Strategy: estrategia, Adaptation: adaptacao,
			BodyWeightKg: in.BodyWeightKg, NowISO: in.NowISO,
		})
		if proxima.CalorieTarget != caso.Applied.CalorieTarget ||
			proxima.Macros != caso.Applied.Macros ||
			proxima.BasisTDEE != caso.Applied.BasisTDEE ||
			proxima.BasisSource != caso.Applied.BasisSource ||
			!reflect.DeepEqual(seguranca, naoNilSinais(caso.Applied.Safety)) {
			falhas++
			if falhas <= 5 {
				t.Errorf("caso %d — aplicada\n  Go: %d kcal %+v base=%d/%s seg=%v\n  TS: %d kcal %+v base=%d/%s seg=%v",
					i, proxima.CalorieTarget, proxima.Macros, proxima.BasisTDEE, proxima.BasisSource, seguranca,
					caso.Applied.CalorieTarget, caso.Applied.Macros, caso.Applied.BasisTDEE, caso.Applied.BasisSource, caso.Applied.Safety)
			}
		}
	}

	if falhas > 0 {
		t.Fatalf("%d de %d casos divergem do TypeScript", falhas, len(casos))
	}
	t.Logf("%d casos comparados com a implementação TypeScript", len(casos))
}

func TestParidadeInvariantesDaEstrategia(t *testing.T) {
	c := DefaultConfig()
	for _, caso := range lerCasosDeCiclo(t).Safety {
		got := CheckStrategySafety(c, Strategy2{
			GoalType: LoseFat, CalorieTarget: caso.In.CalorieTarget, Macros: caso.In.Macros,
			MealsPerDay: 3, EnergyAdjustment: -300, BasisTDEE: 2300, BasisSource: "estimated",
		}, caso.In.BodyWeightKg)
		if !reflect.DeepEqual(got, naoNilSinais(caso.Out)) {
			t.Errorf("%d kcal %+v:\n  Go: %v\n  TS: %v", caso.In.CalorieTarget, caso.In.Macros, got, caso.Out)
		}
	}
}

/*
 * Os mesmos registos que o gerador escreveu, reconstruídos aqui.
 *
 * Guardar milhares de registos no ficheiro era guardar dados a fingir que era
 * contrato: o que importa é a regra que os lê, e a regra que os fabrica tem de
 * ser a mesma dos dois lados — por isso está escrita nos dois.
 */
func registosDeEnsaio(dias int, inicioISO string, kcalPorDia, proteinaPorDia float64) []Log {
	inicio, err := time.Parse(time.RFC3339, normalizarISO(inicioISO))
	if err != nil {
		return nil
	}
	const refeicoes = 3
	out := make([]Log, 0, dias*refeicoes)
	for d := 0; d < dias; d++ {
		for m := 0; m < refeicoes; m++ {
			momento := inicio.AddDate(0, 0, d).Add(time.Duration(m) * time.Hour)
			out = append(out, Log{
				RecordedAtISO: momento.UTC().Format("2006-01-02T15:04:05.000Z"),
				Status:        "eaten",
				Kcal:          kcalPorDia / refeicoes,
				Macros:        MacrosFloat{Protein: proteinaPorDia / refeicoes, Carbs: 40, Fat: 15},
				Portion:       1,
			})
		}
	}
	return out
}

func mesmoInteiro(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func mostrar(v *int) any {
	if v == nil {
		return "nulo"
	}
	return *v
}

func naoNilSinais(s []Signal) []Signal {
	if s == nil {
		return []Signal{}
	}
	return s
}

func naoNilMacros(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}
