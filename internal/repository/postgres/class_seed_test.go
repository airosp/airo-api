package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/airosp/airo-api/internal/engine/training"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func aulasCarregadas(t *testing.T) (*repo.ClassRepo, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}
	r := repo.NewClassRepo(repo.NewTxManager(pool))
	if _, err := r.Seed(ctx); err != nil {
		t.Fatalf("carregar aulas: %v", err)
	}
	return r, pool, ctx
}

// O seed entra, e entra publicado — uma aula por publicar não aparece a
// ninguém, e um catálogo invisível é o mesmo que nenhum.
func TestSeedDeAulasEntraPublicado(t *testing.T) {
	r, _, ctx := aulasCarregadas(t)

	catalogo, err := training.Classes()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogo) != 15 {
		t.Fatalf("o catálogo tem %d aulas, esperava 15", len(catalogo))
	}

	aulas, err := r.Published(ctx, repo.ClassFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(aulas) != len(catalogo) {
		t.Fatalf("%d aulas publicadas, esperava %d", len(aulas), len(catalogo))
	}

	// Cada aula tem de ser servível pelo identificador: é por aí que o ecrã da
	// aula a abre.
	for _, c := range catalogo {
		got, err := r.Get(ctx, c.ID)
		if err != nil {
			t.Fatalf("abrir a aula %q: %v", c.ID, err)
		}
		if got.VideoURL != c.VideoURL {
			t.Errorf("aula %q: vídeo %q, esperava %q", c.ID, got.VideoURL, c.VideoURL)
		}
		if got.DurationSeconds != c.DurationSeconds {
			t.Errorf("aula %q: duração %d, esperava %d", c.ID, got.DurationSeconds, c.DurationSeconds)
		}
	}
}

// Correr o seed outra vez actualiza; não duplica nem republica.
//
// A segunda metade é a que importa: uma aula tirada do ar à mão tem de lá ficar.
// Se o deploy a republicasse, "despublicar" não seria uma decisão — era um
// adiamento até ao arranque seguinte.
func TestSeedDeAulasEIdempotenteENaoRepublica(t *testing.T) {
	r, pool, ctx := aulasCarregadas(t)

	catalogo, _ := training.Classes()
	fora := catalogo[0].ID
	if _, err := pool.Exec(ctx,
		`UPDATE workout_class SET published = false WHERE id = $1`, fora); err != nil {
		t.Fatal(err)
	}

	n, err := r.Seed(ctx)
	if err != nil {
		t.Fatalf("segundo seed: %v", err)
	}
	if n != len(catalogo) {
		t.Fatalf("o seed devolveu %d, esperava %d", n, len(catalogo))
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workout_class`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != len(catalogo) {
		t.Errorf("%d linhas depois de dois seeds — duplicou", total)
	}

	var publicada bool
	if err := pool.QueryRow(ctx,
		`SELECT published FROM workout_class WHERE id = $1`, fora).Scan(&publicada); err != nil {
		t.Fatal(err)
	}
	if publicada {
		t.Errorf("a aula %q voltou a ser publicada pelo seed", fora)
	}
}

// Há sempre uma aula para quem não tem material nenhum, em qualquer foco.
//
// Sem isto, o catálogo parecia cheio e estava vazio para metade das pessoas: o
// `ForDay` não encontrava aula que servisse e o dia caía sempre no plano.
func TestHaAulaSemMaterialParaCadaFoco(t *testing.T) {
	r, _, ctx := aulasCarregadas(t)

	for _, foco := range []string{"upper", "lower", "cardio", "full", "mobility"} {
		// Iniciante é o tecto mais baixo: o que passa aqui passa em todos.
		c, err := r.ForDay(ctx, foco, "beginner", []string{}, 0)
		if err != nil {
			t.Errorf("foco %q: sem aula para quem não tem material (%v)", foco, err)
			continue
		}
		if len(c.Equipment) != 0 {
			t.Errorf("foco %q: a aula %q pede %v a quem não tem nada", foco, c.ID, c.Equipment)
		}
	}
}

// A escolha do dia é determinística: o mesmo dia dá a mesma aula.
func TestAAulaDoDiaNaoMudaEntreChamadas(t *testing.T) {
	r, _, ctx := aulasCarregadas(t)

	primeira, err := r.ForDay(ctx, "mobility", "advanced", nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		outra, err := r.ForDay(ctx, "mobility", "advanced", nil, 7)
		if err != nil {
			t.Fatal(err)
		}
		if outra.ID != primeira.ID {
			t.Fatalf("a aula do dia mudou: %q depois de %q", outra.ID, primeira.ID)
		}
	}
}
