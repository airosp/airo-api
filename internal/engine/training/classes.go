package training

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// As aulas gravadas vivem num JSON embutido, pela mesma razão que os
// exercícios: são catálogo, não lógica. O motor não as monta — serve-as.
//
// Uma aula é uma sessão que já aconteceu uma vez à frente de uma câmara e que
// se repete. Quem decidiu os exercícios, as séries e os descansos foi quem a
// filmou; a app serve o vídeo e grava que aconteceu (docs/decisoes.md D15).
//
//go:embed data/classes.json
var classesJSON []byte

// Class é uma aula gravada, tal como está no catálogo.
type Class struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Specialist string     `json:"specialist"`
	Focus      Focus      `json:"focus"`
	Level      Experience `json:"level"`

	// DurationSeconds é a duração **do vídeo**, e é ela que conta como tempo
	// planeado — ver D15. Trocar o vídeo sem trocar este número é dizer que
	// uma aula de dois minutos pedia trinta, e com isso decidir sozinho se o
	// treino contou.
	DurationSeconds int `json:"durationSeconds"`
	Kcal            int `json:"kcal"`

	VideoURL     string `json:"videoUrl"`
	ThumbnailURL string `json:"thumbnailUrl"`

	// As dimensões do vídeo, medidas no ficheiro.
	//
	// Existem para o cliente reservar o espaço certo **antes** de carregar.
	// Sem elas adivinhava pela miniatura e corrigia depois — e numa playlist,
	// onde se troca de vídeo sem sair do ecrã, isso é a página a mexer-se a
	// cada troca. Zero quer dizer "não medido", e aí o cliente volta a
	// descobrir ao carregar.
	Width  int `json:"width"`
	Height int `json:"height"`

	Summary   string   `json:"summary"`
	Muscles   []string `json:"muscles"`
	Equipment []string `json:"equipment"`

	// Credit é a proveniência do vídeo. **Não vai para a tabela**: não há
	// coluna para ele e não é preciso para servir a aula. Fica aqui porque a
	// proveniência tem de viajar com o ficheiro — quando estas forem
	// substituídas por aulas filmadas pela Airo, é este campo que diz quais
	// ainda não são.
	Credit string `json:"credit"`
}

var (
	classesOnce sync.Once
	classList   []Class
	classesErr  error
)

var focosValidos = map[Focus]bool{
	FocusUpper: true, FocusLower: true, FocusCardio: true,
	FocusFull: true, FocusMobility: true,
}

var niveisValidos = map[Experience]bool{
	Beginner: true, Intermediate: true, Advanced: true,
}

/*
 * A validação acontece aqui e não no INSERT.
 *
 * `focus` e `level` são enums do Postgres: um valor errado rebenta com um erro
 * de conversão a meio do seed, com metade das aulas dentro e metade fora. Ler
 * o ficheiro inteiro primeiro e falhar com o identificador da aula é a
 * diferença entre "invalid input value for enum session_focus" e "aula
 * \"hiit-no-parque\": foco desconhecido".
 */
func loadClasses() {
	classesOnce.Do(func() {
		if err := json.Unmarshal(classesJSON, &classList); err != nil {
			classesErr = fmt.Errorf("catálogo de aulas ilegível: %w", err)
			return
		}
		vistos := make(map[string]bool, len(classList))
		for _, c := range classList {
			switch {
			case c.ID == "":
				classesErr = fmt.Errorf("aula sem identificador")
			case vistos[c.ID]:
				classesErr = fmt.Errorf("aula repetida no catálogo: %q", c.ID)
			case c.Title == "":
				classesErr = fmt.Errorf("aula %q: sem título", c.ID)
			case c.Specialist == "":
				classesErr = fmt.Errorf("aula %q: sem especialista", c.ID)
			case !focosValidos[c.Focus]:
				classesErr = fmt.Errorf("aula %q: foco desconhecido %q", c.ID, c.Focus)
			case !niveisValidos[c.Level]:
				classesErr = fmt.Errorf("aula %q: nível desconhecido %q", c.ID, c.Level)
			case c.DurationSeconds <= 0:
				classesErr = fmt.Errorf("aula %q: duração tem de ser positiva", c.ID)
			case c.VideoURL == "":
				classesErr = fmt.Errorf("aula %q: sem endereço de vídeo", c.ID)
			// Uma das duas sem a outra não dá proporção nenhuma, e passaria
			// despercebida até alguém reparar no ecrã a saltar.
			case (c.Width == 0) != (c.Height == 0):
				classesErr = fmt.Errorf("aula %q: largura e altura têm de vir as duas ou nenhuma", c.ID)
			}
			if classesErr != nil {
				return
			}
			vistos[c.ID] = true
		}
	})
}

// Classes devolve o catálogo de aulas. A ordem é a do ficheiro.
func Classes() ([]Class, error) {
	loadClasses()
	if classesErr != nil {
		return nil, classesErr
	}
	out := make([]Class, len(classList))
	copy(out, classList)
	return out, nil
}
