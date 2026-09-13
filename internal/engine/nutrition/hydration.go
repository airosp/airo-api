package nutrition

import "github.com/airosp/airo-api/internal/engine/portable"

// Quanta água beber num dia.
//
// ⚠️ **Estes números não estão validados clinicamente**, como os restantes
// limiares deste motor. Ficam em configuração e não como constantes: mudam sem
// nova versão da aplicação, que é o que permite a um profissional corrigi-los.
//
// O que substituem é pior: um alvo fixo de 2,5 L, igual para quem pesa 55 kg e
// para quem pesa 95 kg, e igual num dia de descanso e num de noventa minutos.

// HydrationInput é o que a conta precisa.
type HydrationInput struct {
	BodyWeightKg float64
	TrainsToday  bool
	// SessionMinutes é a duração do treino de hoje. Zero em dia de descanso.
	SessionMinutes int
}

// HydrationTarget devolve o alvo do dia em mililitros.
//
// Arredonda a 100 ml porque ninguém mede 2 347 ml: um alvo com precisão a mais
// parece uma medida e é uma estimativa.
func HydrationTarget(c Config, in HydrationInput) int {
	if in.BodyWeightKg <= 0 {
		return c.Hydration.FallbackMl
	}

	ml := in.BodyWeightKg * c.Hydration.MlPerKg
	if in.TrainsToday && in.SessionMinutes > 0 {
		ml += float64(in.SessionMinutes) / 60 * c.Hydration.MlPerTrainingHour
	}

	alvo := int(portable.RoundJS(ml/100)) * 100
	if alvo < c.Hydration.MinMl {
		return c.Hydration.MinMl
	}
	if alvo > c.Hydration.MaxMl {
		// Um tecto existe porque beber demais não é inofensivo: acima de um
		// certo ponto dilui-se o sódio, e o alvo deixava de ser um conselho.
		return c.Hydration.MaxMl
	}
	return alvo
}
