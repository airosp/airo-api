package postgres

import (
	"testing"
	"testing/fstest"
)

func TestLoadOrdersByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0010_dez.up.sql":    {Data: []byte("SELECT 10;")},
		"0010_dez.down.sql":  {Data: []byte("SELECT -10;")},
		"0002_dois.up.sql":   {Data: []byte("SELECT 2;")},
		"0002_dois.down.sql": {Data: []byte("SELECT -2;")},
	}
	migs, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	// Ordem numérica, não alfabética: "0010" antes de "0002" por texto estaria
	// errado, e é o erro clássico de quem ordena nomes de ficheiro.
	if len(migs) != 2 || migs[0].Version != 2 || migs[1].Version != 10 {
		t.Fatalf("ordem errada: %+v", migs)
	}
	if migs[0].Name != "dois" {
		t.Fatalf("nome = %q", migs[0].Name)
	}
}

func TestLoadRequiresDown(t *testing.T) {
	fsys := fstest.MapFS{"0001_init.up.sql": {Data: []byte("SELECT 1;")}}
	if _, err := Load(fsys); err == nil {
		t.Fatal("uma migração sem .down devia falhar: não se desfaz o que não tem volta")
	}
}

func TestLoadRejectsDuplicateVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_a.up.sql": {Data: []byte("SELECT 1;")}, "0001_a.down.sql": {Data: []byte("")},
		"0001_b.up.sql": {Data: []byte("SELECT 2;")}, "0001_b.down.sql": {Data: []byte("")},
	}
	if _, err := Load(fsys); err == nil {
		t.Fatal("duas migrações com a mesma versão deviam falhar")
	}
}

func TestChecksumChangesWithContent(t *testing.T) {
	mk := func(sql string) string {
		m, err := Load(fstest.MapFS{
			"0001_init.up.sql":   {Data: []byte(sql)},
			"0001_init.down.sql": {Data: []byte("")},
		})
		if err != nil {
			t.Fatal(err)
		}
		return m[0].Checksum
	}
	if mk("SELECT 1;") == mk("SELECT 2;") {
		t.Fatal("checksums iguais para conteúdos diferentes — editar uma migração aplicada passaria despercebido")
	}
}

// A migração real tem de ser legível e emparelhada.
func TestRealMigrationsLoad(t *testing.T) {
	migs, err := Load(realMigrations(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 {
		t.Fatal("nenhuma migração encontrada")
	}
	if migs[0].Version != 1 || migs[0].Name != "init" {
		t.Fatalf("a primeira devia ser 0001_init, é %04d_%s", migs[0].Version, migs[0].Name)
	}
}
