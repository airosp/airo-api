package admin

import (
	"bytes"
	"testing"
)

func TestMatrix(t *testing.T) {
	if !Can(RoleAdmin, AreaTeam, Edit) || Can(RoleContent, AreaTeam, View) {
		t.Fatal("só a administração gere a equipa")
	}
	for _, r := range Roles {
		if Can(r, AreaUserRecord, Edit) {
			t.Fatalf("%s não pode editar a ficha de saúde", r)
		}
	}
	if Can(RoleSupport, AreaClasses, Edit) || !Can(RoleSupport, AreaClasses, View) {
		t.Fatal("o suporte vê as aulas, não as muda")
	}
}

func TestToken(t *testing.T) {
	a, ha, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewToken()
	if a == b || !bytes.Equal(ha, HashToken(a)) || bytes.Equal(ha, HashToken(b)) {
		t.Fatal("tokens")
	}
}
