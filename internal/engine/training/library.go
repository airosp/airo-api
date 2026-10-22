package training

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
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

/*
 * O catálogo gravado, quando o há.
 *
 * O JSON embutido é o ponto de partida; a partir do momento em que a equipa
 * edita um exercício no painel, a verdade passa a ser a base de dados. A API
 * carrega-a no arranque e depois de cada edição, e troca-a aqui de uma vez —
 * o motor continua sem I/O: recebe uma lista, não vai buscá-la.
 */
type libraryState struct {
	list []Exercise
	byID map[string]Exercise
}

var gravada atomic.Pointer[libraryState]

// ReplaceLibrary troca o catálogo pelo gravado. Recusa-o inteiro se houver
// ids vazios ou repetidos: meio catálogo é pior do que o anterior.
func ReplaceLibrary(list []Exercise) error {
	if len(list) == 0 {
		return fmt.Errorf("catálogo de exercícios vazio")
	}
	idx := make(map[string]Exercise, len(list))
	for _, e := range list {
		if e.ID == "" {
			return fmt.Errorf("exercício sem id no catálogo gravado")
		}
		if _, dup := idx[e.ID]; dup {
			return fmt.Errorf("exercício repetido no catálogo gravado: %q", e.ID)
		}
		idx[e.ID] = e
	}
	copia := make([]Exercise, len(list))
	copy(copia, list)
	gravada.Store(&libraryState{list: copia, byID: idx})
	return nil
}

// ResetLibrary volta ao JSON embutido. Só os testes o usam.
func ResetLibrary() { gravada.Store(nil) }

// EmbeddedLibrary é o JSON embutido, venha o que vier da base de dados. É o
// que o seed grava.
func EmbeddedLibrary() ([]Exercise, error) {
	loadLibrary()
	if libraryErr != nil {
		return nil, libraryErr
	}
	out := make([]Exercise, len(library))
	copy(out, library)
	return out, nil
}

// Library devolve o catálogo. A ordem é a do ficheiro, e importa: a escolha por
// semente indexa sobre ela.
func Library() ([]Exercise, error) {
	if g := gravada.Load(); g != nil {
		out := make([]Exercise, len(g.list))
		copy(out, g.list)
		return out, nil
	}
	loadLibrary()
	if libraryErr != nil {
		return nil, libraryErr
	}
	out := make([]Exercise, len(library))
	copy(out, library)
	return out, nil
}

func GetExercise(id string) (Exercise, bool) {
	if g := gravada.Load(); g != nil {
		e, ok := g.byID[id]
		return e, ok
	}
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
