package training

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// O catálogo de especialistas, embutido como os outros.
//
// Vivia só no cliente (`constants/specialists.ts`) e sobe para cá pela razão da
// D15: enquanto lá viveu, o campo `specialist` de uma aula era texto que ninguém
// garantia existir.
//
//go:embed data/specialists.json
var specialistsJSON []byte

type SpecialistRole string

const (
	RoleTrainer      SpecialistRole = "trainer"
	RoleNutritionist SpecialistRole = "nutritionist"
	RolePhysio       SpecialistRole = "physio"
	RoleCoach        SpecialistRole = "coach"
)

type Specialist struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Role     SpecialistRole `json:"role"`
	Headline string         `json:"headline"`
	Bio      string         `json:"bio"`
	Tags     []string       `json:"tags"`

	Rating       float64  `json:"rating"`
	Clients      int      `json:"clients"`
	ResponseTime string   `json:"responseTime"`
	Initials     string   `json:"initials"`
	Gradient     []string `json:"gradient"`

	// RecommendedFor são os objectivos para os quais aparece com o selo
	// "sugerido para ti".
	RecommendedFor []string `json:"recommendedFor"`
}

var (
	specialistsOnce sync.Once
	specialistList  []Specialist
	specialistsErr  error
)

var papeisValidos = map[SpecialistRole]bool{
	RoleTrainer: true, RoleNutritionist: true, RolePhysio: true, RoleCoach: true,
}

func loadSpecialists() {
	specialistsOnce.Do(func() {
		if err := json.Unmarshal(specialistsJSON, &specialistList); err != nil {
			specialistsErr = fmt.Errorf("catálogo de especialistas ilegível: %w", err)
			return
		}
		vistos := make(map[string]bool, len(specialistList))
		for _, s := range specialistList {
			switch {
			case s.ID == "":
				specialistsErr = fmt.Errorf("especialista sem identificador")
			case vistos[s.ID]:
				specialistsErr = fmt.Errorf("especialista repetido: %q", s.ID)
			case s.Name == "":
				specialistsErr = fmt.Errorf("especialista %q: sem nome", s.ID)
			case !papeisValidos[s.Role]:
				specialistsErr = fmt.Errorf("especialista %q: papel desconhecido %q", s.ID, s.Role)
			case len(s.Gradient) != 2:
				// O avatar é um gradiente de duas cores; uma só não é gradiente
				// e três não cabem no desenho.
				specialistsErr = fmt.Errorf("especialista %q: o gradiente precisa de duas cores", s.ID)
			}
			if specialistsErr != nil {
				return
			}
			vistos[s.ID] = true
		}
	})
}

// Specialists devolve o catálogo. A ordem é a do ficheiro, e é a que o ecrã usa.
func Specialists() ([]Specialist, error) {
	loadSpecialists()
	if specialistsErr != nil {
		return nil, specialistsErr
	}
	out := make([]Specialist, len(specialistList))
	copy(out, specialistList)
	return out, nil
}
