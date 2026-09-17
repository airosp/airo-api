package training

import "testing"

// O catálogo lê-se, e tem os oito que o cliente mostrava.
func TestOCatalogoDeEspecialistasLeSe(t *testing.T) {
	lista, err := Specialists()
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 8 {
		t.Fatalf("%d especialistas, esperava 8", len(lista))
	}

	// Um de cada papel, no mínimo: o assistente oferece os quatro.
	papeis := map[SpecialistRole]int{}
	for _, s := range lista {
		papeis[s.Role]++
		if s.Initials == "" || len(s.Gradient) != 2 {
			t.Errorf("%q: sem o que desenhar o avatar", s.ID)
		}
	}
	for _, p := range []SpecialistRole{RoleTrainer, RoleNutritionist, RolePhysio, RoleCoach} {
		if papeis[p] == 0 {
			t.Errorf("nenhum especialista com o papel %q", p)
		}
	}
}

// As aulas apontam para especialistas que existem — é o que a chave estrangeira
// da migração 19 passa a garantir, e o que aqui se verifica antes de lá chegar.
func TestAsAulasApontamParaEspecialistasQueExistem(t *testing.T) {
	lista, err := Specialists()
	if err != nil {
		t.Fatal(err)
	}
	existe := map[string]bool{}
	for _, s := range lista {
		existe[s.ID] = true
	}

	aulas, err := Classes()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range aulas {
		if !existe[a.Specialist] {
			t.Errorf("aula %q dá-se por dada pelo %q, que não está no catálogo", a.ID, a.Specialist)
		}
	}
}
