package policy

import "github.com/airosp/airo-api/internal/intelligence/contracts"

/*
 * Features são os interruptores, um por funcionalidade.
 *
 * `Enabled` desliga tudo de uma vez — é o botão de emergência. Os outros
 * ligam-se um a um: a fotografia pode estar ligada e o texto não, enquanto se
 * mede a qualidade de cada um com pessoas a sério.
 */
type Features struct {
	Enabled   bool
	MealPhoto bool
	MealText  bool
}

// Allows diz se a tarefa pode correr nesta instalação.
func (f Features) Allows(task string) bool {
	if !f.Enabled {
		return false
	}
	switch task {
	case contracts.TaskMealPhoto:
		return f.MealPhoto
	case contracts.TaskMealText:
		return f.MealText
	}
	return false
}
