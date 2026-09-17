package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
 * A jornada avança porque o tempo passou, e não porque alguém abriu a app.
 *
 * ⚠️ Um ciclo nascia com a jornada e nunca acabava: quem passasse duas semanas
 * sem abrir voltava a um plano parado no tempo.
 */

func baseComJornada(t *testing.T, semanas int, inicio time.Time) (*pgxpool.Pool, string) {
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

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841112000','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var goalID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO goal (user_id, type, horizon, direction, priority, status)
		 VALUES ($1,'outcome','open_ended','lose_weight','weight','active') RETURNING id`,
		userID).Scan(&goalID); err != nil {
		t.Fatal(err)
	}
	var journeyID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, cycle_weeks, status)
		 VALUES ($1,'open_ended',$2::date,$3,'active') RETURNING id`,
		goalID, inicio, semanas).Scan(&journeyID); err != nil {
		t.Fatal(err)
	}
	// O ciclo que nasce com a jornada.
	if _, err := pool.Exec(ctx,
		`INSERT INTO cycle (journey_id, index, start_date, review_date)
		 VALUES ($1, 1, $2::date, ($2::date + $3::int))`,
		journeyID, inicio, semanas*7); err != nil {
		t.Fatal(err)
	}
	return pool, journeyID
}

func ciclos(t *testing.T, pool *pgxpool.Pool, journeyID string) (abertos, total int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE closed_at IS NULL), count(*)
		   FROM cycle WHERE journey_id = $1`, journeyID).Scan(&abertos, &total); err != nil {
		t.Fatal(err)
	}
	return abertos, total
}

// Passada a data de revisão, o ciclo fecha e abre o seguinte.
func TestOCicloViraQuandoADataChega(t *testing.T) {
	inicio := time.Now().UTC().AddDate(0, 0, -30)
	pool, journeyID := baseComJornada(t, 4, inicio)
	r := repo.NewCiclosRepo(repo.NewTxManager(pool))

	out, err := r.Rolar(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if out.Abertos == 0 {
		t.Fatal("não virou nada")
	}
	abertos, total := ciclos(t, pool, journeyID)
	if abertos != 1 {
		t.Errorf("ficaram %d ciclos abertos", abertos)
	}
	if total < 2 {
		t.Errorf("só há %d ciclos", total)
	}

	indice, _, revisao, err := r.CicloActual(context.Background(), journeyID)
	if err != nil {
		t.Fatal(err)
	}
	if indice < 2 {
		t.Errorf("o ciclo actual é o %d", indice)
	}
	if !revisao.After(time.Now().UTC()) {
		t.Errorf("a revisão do ciclo novo já passou: %s", revisao.Format("2006-01-02"))
	}
}

/*
 * Quem desapareceu três meses volta ao ciclo de hoje, e não ao de Junho.
 *
 * É o caso que interessa: a app avança sozinha precisamente para quem não a
 * abriu.
 */
func TestApanhaQuemEstaVariosCiclosAtrasado(t *testing.T) {
	inicio := time.Now().UTC().AddDate(0, 0, -90)
	pool, journeyID := baseComJornada(t, 4, inicio)
	r := repo.NewCiclosRepo(repo.NewTxManager(pool))

	if _, err := r.Rolar(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	indice, _, revisao, err := r.CicloActual(context.Background(), journeyID)
	if err != nil {
		t.Fatal(err)
	}
	if indice < 4 {
		t.Errorf("noventa dias a quatro semanas dão pelo menos o quarto ciclo; veio o %d", indice)
	}
	if !revisao.After(time.Now().UTC()) {
		t.Errorf("a revisão do ciclo actual já passou: %s", revisao.Format("2006-01-02"))
	}
	abertos, _ := ciclos(t, pool, journeyID)
	if abertos != 1 {
		t.Errorf("%d ciclos abertos ao mesmo tempo", abertos)
	}
}

// Correr duas vezes não muda nada na segunda.
func TestRolarDuasVezesNaoDuplica(t *testing.T) {
	inicio := time.Now().UTC().AddDate(0, 0, -30)
	pool, journeyID := baseComJornada(t, 4, inicio)
	r := repo.NewCiclosRepo(repo.NewTxManager(pool))
	ctx := context.Background()

	if _, err := r.Rolar(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, totalDepoisDaPrimeira := ciclos(t, pool, journeyID)

	segunda, err := r.Rolar(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if segunda.Abertos != 0 {
		t.Errorf("a segunda passagem abriu %d ciclos", segunda.Abertos)
	}
	if _, total := ciclos(t, pool, journeyID); total != totalDepoisDaPrimeira {
		t.Errorf("de %d para %d ciclos", totalDepoisDaPrimeira, total)
	}
}

// Um ciclo cuja revisão ainda não chegou fica quieto.
func TestCicloPorVencerFicaQuieto(t *testing.T) {
	inicio := time.Now().UTC().AddDate(0, 0, -3)
	pool, journeyID := baseComJornada(t, 4, inicio)
	r := repo.NewCiclosRepo(repo.NewTxManager(pool))

	out, err := r.Rolar(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if out.Abertos != 0 {
		t.Fatalf("virou %d ciclos antes da data", out.Abertos)
	}
	if _, total := ciclos(t, pool, journeyID); total != 1 {
		t.Errorf("há %d ciclos", total)
	}
}

/*
 * Uma jornada em pausa não avança.
 *
 * Quem carregou em pausa não quer voltar e encontrar quatro ciclos passados à
 * espera — quer voltar onde estava.
 */
func TestJornadaEmPausaNaoAvanca(t *testing.T) {
	inicio := time.Now().UTC().AddDate(0, 0, -30)
	pool, journeyID := baseComJornada(t, 4, inicio)
	if _, err := pool.Exec(context.Background(),
		`UPDATE journey SET status = 'paused' WHERE id = $1`, journeyID); err != nil {
		t.Fatal(err)
	}

	r := repo.NewCiclosRepo(repo.NewTxManager(pool))
	out, err := r.Rolar(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if out.Abertos != 0 {
		t.Fatalf("avançou %d ciclos numa jornada em pausa", out.Abertos)
	}
}
