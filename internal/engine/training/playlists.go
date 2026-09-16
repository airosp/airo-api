package training

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// As playlists vivem num JSON embutido, como as aulas e os exercícios: são
// catálogo, não lógica.
//
// Uma playlist é uma sequência de aulas com um objetivo — e a sequência é
// **curada**, não gerada. O motor não a compõe: quem produz os vídeos monta a
// ordem e etiqueta-a. É a mesma decisão que se tomou para as aulas (D15), pela
// mesma razão: numa aula lidera o vídeo, numa playlist lidera a ordem que
// alguém pensou.
//
//go:embed data/playlists.json
var playlistsJSON []byte

// Playlist é uma sequência de aulas, tal como está no catálogo.
type Playlist struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`

	// Goals são os objetivos do perfil a que esta lista serve — os mesmos
	// nomes que o cliente usa (`strength|fatLoss|muscle|habit`). Uma lista
	// pode servir mais do que um.
	Goals []string `json:"goals"`
	// Zones são as zonas do corpo em linguagem de quem treina ("Cintura").
	// Servem para procurar e para explicar; nunca entram numa conta.
	Zones []string `json:"zones"`

	Level Experience `json:"level"`
	// Items são as aulas **pela ordem em que se fazem**.
	Items []PlaylistItem `json:"items"`

	CoverURL string `json:"coverUrl"`
}

// PlaylistItem é uma aula no seu lugar da sequência.
type PlaylistItem struct {
	// Class é o identificador da aula no catálogo.
	Class string `json:"class"`
	/*
	 * Subtitle é o papel da aula **nesta** lista: "Aquecimento · Cardio".
	 *
	 * Não se deduz do foco do vídeo. A mesma aula de cardio é aquecimento numa
	 * lista e trabalho principal noutra — quem decide é quem curou a sequência,
	 * e é por isso que o texto vem escrito daqui.
	 */
	Subtitle string `json:"subtitle"`
}

var (
	playlistsOnce sync.Once
	playlistList  []Playlist
	playlistsErr  error
)

/*
 * A validação acontece aqui, e inclui as referências às aulas.
 *
 * É a única altura em que as duas listas estão à mão ao mesmo tempo. Uma
 * playlist que aponta para uma aula que não existe só dava erro à frente — no
 * seed, com metade das listas dentro, ou pior: em silêncio, com uma lista de
 * quatro aulas a servir três. Falhar aqui diz qual é a lista e qual é a aula.
 */
func loadPlaylists() {
	playlistsOnce.Do(func() {
		if err := json.Unmarshal(playlistsJSON, &playlistList); err != nil {
			playlistsErr = fmt.Errorf("catálogo de playlists ilegível: %w", err)
			return
		}

		aulas, err := Classes()
		if err != nil {
			playlistsErr = err
			return
		}
		existe := make(map[string]bool, len(aulas))
		for _, c := range aulas {
			existe[c.ID] = true
		}

		vistas := make(map[string]bool, len(playlistList))
		for _, p := range playlistList {
			switch {
			case p.ID == "":
				playlistsErr = fmt.Errorf("playlist sem identificador")
			case vistas[p.ID]:
				playlistsErr = fmt.Errorf("playlist repetida no catálogo: %q", p.ID)
			case p.Title == "":
				playlistsErr = fmt.Errorf("playlist %q: sem título", p.ID)
			case !niveisValidos[p.Level]:
				playlistsErr = fmt.Errorf("playlist %q: nível desconhecido %q", p.ID, p.Level)
			case len(p.Items) == 0:
				playlistsErr = fmt.Errorf("playlist %q: sem aulas", p.ID)
			}
			if playlistsErr != nil {
				return
			}
			for i, item := range p.Items {
				if !existe[item.Class] {
					playlistsErr = fmt.Errorf(
						"playlist %q, posição %d: a aula %q não existe no catálogo", p.ID, i+1, item.Class)
					return
				}
				if item.Subtitle == "" {
					playlistsErr = fmt.Errorf(
						"playlist %q, posição %d: sem legenda — a lista não diria para que serve a aula", p.ID, i+1)
					return
				}
			}
			vistas[p.ID] = true
		}
	})
}

// Playlists devolve o catálogo de playlists. A ordem é a do ficheiro.
func Playlists() ([]Playlist, error) {
	loadPlaylists()
	if playlistsErr != nil {
		return nil, playlistsErr
	}
	out := make([]Playlist, len(playlistList))
	copy(out, playlistList)
	return out, nil
}
