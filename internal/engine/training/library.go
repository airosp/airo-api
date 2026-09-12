package training

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// A biblioteca vive num JSON embutido e não em código Go.
//
// É catálogo, não lógica: muda com muito mais frequência do que o motor, e o
// mesmo ficheiro serve de seed da tabela `exercise` e de fonte de verdade para
// comparar o porte com o cliente.
//
//go:embed data/exercises.json
var exercisesJSON []byte

var (
	libraryOnce sync.Once
	library     []Exercise
	libraryErr  error
	byID        map[string]Exercise
)

func loadLibrary() {
	libraryOnce.Do(func() {
		if err := json.Unmarshal(exercisesJSON, &library); err != nil {
			libraryErr = fmt.Errorf("biblioteca de exercícios ilegível: %w", err)
			return
		}
		byID = make(map[string]Exercise, len(library))
		for _, e := range library {
			if _, dup := byID[e.ID]; dup {
				libraryErr = fmt.Errorf("exercício repetido na biblioteca: %q", e.ID)
				return
			}
			byID[e.ID] = e
		}
	})
}

// Library devolve o catálogo. A ordem é a do ficheiro, e importa: a escolha por
// semente indexa sobre ela.
func Library() ([]Exercise, error) {
	loadLibrary()
	if libraryErr != nil {
		return nil, libraryErr
	}
	out := make([]Exercise, len(library))
	copy(out, library)
	return out, nil
}

func GetExercise(id string) (Exercise, bool) {
	loadLibrary()
	if libraryErr != nil {
		return Exercise{}, false
	}
	e, ok := byID[id]
	return e, ok
}

var levelRank = map[Experience]int{Beginner: 0, Intermediate: 1, Advanced: 2}

// isAvailable — sem lista de equipamento, basta o corpo.
func isAvailable(e Exercise, equipment []string) bool {
	if len(e.Equipment) == 0 {
		return true
	}
	for _, need := range e.Equipment {
		for _, have := range equipment {
			if need == have {
				return true
			}
		}
	}
	return false
}
