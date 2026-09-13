package training

import "time"

// PlanLabels é a rotação dos dias de treino da semana.
//
// Espelha `buildWeeklyPlan` em `mobile/context/AiroContext.tsx`. Está aqui e
// não lá porque o rótulo escolhe o treino, e escolher o treino é uma decisão:
// se viesse no pedido, era o cliente a dizer ao servidor o que queria treinar.
var PlanLabels = []string{"Upper Body", "Lower Body", "Cardio", "Full Body", "Mobility"}

// RecoveryLabel é o rótulo dos dias sem treino marcado.
const RecoveryLabel = "Recovery"

// PlanLabelOn diz o que é hoje, dados os dias de treino da semana.
//
// `workoutDays` são índices com a segunda-feira em 0 — a mesma convenção do
// plano semanal no telemóvel.
//
// A rotação segue a **ordem dos treinos**, não o índice do dia. Quem treina à
// segunda, quarta e sexta faz Upper, Lower, Cardio; contar pelo índice do dia
// saltava "Lower Body" sem razão nenhuma, e a semana passava ao lado de metade
// dos padrões de movimento.
func PlanLabelOn(workoutDays []int, day time.Time) string {
	// time.Weekday tem o domingo em 0; o plano tem a segunda. A conta põe os
	// dois a falar da mesma coisa.
	index := (int(day.Weekday()) + 6) % 7

	if !containsDay(workoutDays, index) {
		return RecoveryLabel
	}

	count := 0
	for i := 0; i < index; i++ {
		if containsDay(workoutDays, i) {
			count++
		}
	}
	return PlanLabels[count%len(PlanLabels)]
}

func containsDay(days []int, index int) bool {
	for _, d := range days {
		if d == index {
			return true
		}
	}
	return false
}
