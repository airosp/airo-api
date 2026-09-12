package nutrition

import (
	"github.com/airosp/airo-api/internal/engine/portable"
)

// CalorieTargetInput é o instantâneo de que o cálculo precisa. Nada mais.
type CalorieTargetInput struct {
	TDEEKcal float64
	GoalType GoalType
	// Aggressiveness: 0 = ritmo confortável, 1 = o máximo que a configuração
	// permite. Fora de [0,1] é limitado, não rejeitado.
	Aggressiveness float64
}

type CalorieTarget struct {
	Target     int
	Adjustment int
	// FlooredAt diz que o piso entrou em acção. Não é o mesmo que o alvo ser
	// 1500 por coincidência, e a interface precisa de saber a diferença para
	// poder explicar porque é que o défice pedido não foi aplicado todo.
	FlooredAt *int
}

// ComputeCalorieTarget é o alvo a partir do TDEE — nunca `peso × 30`.
//
// O ajuste é uma fracção do gasto e não um número fixo: −250 kcal significa
// coisas muito diferentes para quem gasta 1 800 e para quem gasta 3 200.
func ComputeCalorieTarget(c Config, in CalorieTargetInput) CalorieTarget {
	band := c.EnergyAdjustment[in.GoalType]

	a := in.Aggressiveness
	if a < 0 {
		a = 0
	}
	if a > 1 {
		a = 1
	}

	ratio := band.Comfortable + (band.Max-band.Comfortable)*a
	adjustment := int(portable.RoundJS(in.TDEEKcal * ratio))
	raw := int(portable.RoundJS(in.TDEEKcal)) + adjustment

	out := CalorieTarget{Adjustment: adjustment, Target: raw}
	if raw < c.MinDailyCalories {
		floor := c.MinDailyCalories
		out.Target = floor
		out.FlooredAt = &floor
	}
	return out
}

// ObservedTDEE é o gasto que o corpo revelou, e não o que a fórmula previu:
//
//	gasto ≈ ingestão média + (variação de massa × kcal por kg) / dias
//
// É esta função que separa uma calculadora de um sistema adaptativo.
//
// Devolve nil quando não há base para decidir. Um número devolvido a partir de
// três dias de registo é pior do que nenhum: parece informação.
func ObservedTDEE(c Config, averageIntakeKcal, weightChangeKg float64, days int) *int {
	if days < 7 || averageIntakeKcal <= 0 {
		return nil
	}
	fromMass := (-weightChangeKg * c.KcalPerKg) / float64(days)
	estimate := int(portable.RoundJS(averageIntakeKcal + fromMass))

	// Valores absurdos indicam registo incompleto, não metabolismo estranho.
	if estimate < 800 || estimate > 6000 {
		return nil
	}
	return &estimate
}
