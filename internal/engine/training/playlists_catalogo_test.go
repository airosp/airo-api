package training

import "testing"

// O catálogo carrega, e cada aula referida existe mesmo.
//
// A segunda parte é a que importa: uma playlist que aponta para uma aula
// inexistente serviria uma lista mais curta do que a que está escrita, sem
// ninguém dar por isso. `Playlists()` recusa-se a devolver nesse caso, por
// isso um erro aqui **é** a falha de referência.
func TestPlaylistsCarregam(t *testing.T) {
	listas, err := Playlists()
	if err != nil {
		t.Fatalf("catálogo não carrega: %v", err)
	}
	if len(listas) == 0 {
		t.Fatal("catálogo vazio")
	}

	aulas, err := Classes()
	if err != nil {
		t.Fatalf("aulas não carregam: %v", err)
	}
	duracao := make(map[string]int, len(aulas))
	for _, c := range aulas {
		duracao[c.ID] = c.DurationSeconds
	}

	for _, p := range listas {
		if len(p.Items) == 0 {
			t.Errorf("%s: sem aulas", p.ID)
		}
		total := 0
		for i, item := range p.Items {
			d, ok := duracao[item.Class]
			if !ok {
				t.Errorf("%s posição %d: aula %q não existe", p.ID, i+1, item.Class)
			}
			// Sem legenda, a lista não diria para que serve cada aula — e é
			// isso que distingue uma sequência de uma pilha de vídeos.
			if item.Subtitle == "" {
				t.Errorf("%s posição %d (%s): sem legenda", p.ID, i+1, item.Class)
			}
			total += d
		}
		if len(p.Goals) == 0 {
			t.Errorf("%s: sem objetivo — ninguém a encontraria", p.ID)
		}
		t.Logf("%-28s %d aulas · %dm%02ds · %v", p.ID, len(p.Items), total/60, total%60, p.Goals)
	}
}
