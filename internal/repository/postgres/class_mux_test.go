package postgres_test

import (
	"context"
	"testing"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
 * O vídeo de uma aula, do upload até tocar.
 *
 * ⚠️ Uma aula nasce **sem vídeo reproduzível**: o Mux recebe o ficheiro e
 * demora a processá-lo. Entre uma coisa e outra a aula não pode aparecer em
 * lado nenhum — um cartão que abre um leitor que gira para sempre é lido como
 * app partida, não como vídeo a processar.
 */

/*
 * Uma aula à espera do vídeo.
 *
 * É como uma aula nasce: ficha escrita, ficheiro enviado, e o Mux a
 * processar. **Por publicar**, porque uma aula publicada tem de ter o que
 * tocar — é o que o `class_publicada_tem_video` garante.
 */
func aulaPorProcessar(t *testing.T, pool *pgxpool.Pool, ctx context.Context, id string, duracao int) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO workout_class (id, title, specialist, focus, level,
		                            duration_seconds, kcal, video_url, summary,
		                            muscles, equipment, published, mux_policy, mux_ready)
		 VALUES ($1,'Aula nova','rita-campos','upper','beginner',
		         $2,0,'','', '{}','{}', false,'signed',false)`, id, duracao); err != nil {
		t.Fatal(err)
	}
}

// publicar é o gesto de quem produz, depois de o vídeo estar pronto.
func publicar(t *testing.T, pool *pgxpool.Pool, ctx context.Context, id string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE workout_class SET published = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

/*
 * Um vídeo a ser substituído tira a aula da lista até estar pronto.
 *
 * A aula já esteve publicada e tem identificador de reprodução do vídeo
 * antigo; o produtor enviou outro e o Mux está a processar. Enquanto isso, o
 * cartão não pode aparecer: o identificador que lá está ainda aponta para o
 * que está a ser trocado.
 */
func TestAulaSemVideoProcessadoNaoApareceNaLista(t *testing.T) {
	r, pool, ctx := aulasCarregadas(t)
	aulaPorProcessar(t, pool, ctx, "aula-a-processar", 1)
	if _, err := pool.Exec(ctx,
		`UPDATE workout_class SET published = true, mux_playback_id = 'playback-antigo',
		                          mux_ready = false
		   WHERE id = 'aula-a-processar'`); err != nil {
		t.Fatal(err)
	}

	// Uma aula por processar não entra na lista...
	lista, err := r.Published(ctx, repo.ClassFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range lista {
		if c.ID == "aula-a-processar" {
			t.Fatal("uma aula sem vídeo processado não devia aparecer na lista")
		}
	}

	// ...nem é escolhida como a aula do dia.
	if aula, err := r.ForDay(ctx, "upper", "beginner", nil, 0); err == nil && aula.ID == "aula-a-processar" {
		t.Fatal("uma aula sem vídeo processado não devia ser o dia")
	}
}

func TestOWebhookAcordaAAulaEElaPassaAAparecer(t *testing.T) {
	r, pool, ctx := aulasCarregadas(t)
	aulaPorProcessar(t, pool, ctx, "aula-a-processar", 1)

	ok, err := r.MuxPronto(ctx, "aula-a-processar", "asset-1", "playback-1", "signed", 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a aula existia e devia ter sido encontrada pelo passthrough")
	}
	publicar(t, pool, ctx, "aula-a-processar")

	lista, err := r.Published(ctx, repo.ClassFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var aula *repo.ClassRow
	for i := range lista {
		if lista[i].ID == "aula-a-processar" {
			aula = &lista[i]
		}
	}
	if aula == nil {
		t.Fatal("depois de pronta, a aula tem de aparecer")
	}
	if aula.MuxPlaybackID != "playback-1" || !aula.MuxSigned || !aula.MuxReady {
		t.Errorf("estado do vídeo errado: %+v", aula)
	}
	if aula.Width != 1920 || aula.Height != 1080 {
		t.Errorf("dimensões = %dx%d, esperava 1920x1080", aula.Width, aula.Height)
	}
	// A duração é da ficha, sempre: o Mux mede outra coisa (ver D15).
	if aula.DurationSeconds != 1 {
		t.Errorf("duração = %d — a da ficha devia ter ficado intacta", aula.DurationSeconds)
	}
}

/*
 * A duração escrita à mão manda sobre a medida pelo Mux.
 *
 * ⚠️ É ela que decide se o treino contou (D15). O vídeo tem segundos de
 * contagem e de despedida que a aula de propósito não conta como treino, e
 * escrevê-los por cima mudava a adesão de quem a fez.
 */
func TestADuracaoDaFichaNaoEEscritaPorCima(t *testing.T) {
	r, pool, ctx := aulasCarregadas(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO workout_class (id, title, specialist, focus, level,
		                            duration_seconds, kcal, video_url, summary,
		                            muscles, equipment, published, mux_policy, mux_ready)
		 VALUES ('aula-com-duracao','Aula','rita-campos','upper','beginner',
		         1800,0,'','', '{}','{}', false,'signed',false)`); err != nil {
		t.Fatal(err)
	}

	if _, err := r.MuxPronto(ctx, "aula-com-duracao", "asset-2", "playback-2", "signed", 0, 0); err != nil {
		t.Fatal(err)
	}

	publicar(t, pool, ctx, "aula-com-duracao")
	aula, err := r.Get(ctx, "aula-com-duracao")
	if err != nil {
		t.Fatal(err)
	}
	if aula.DurationSeconds != 1800 {
		t.Errorf("duração = %d — a da ficha devia ter ficado", aula.DurationSeconds)
	}
}

// Um vídeo que o Mux não conseguiu processar despublica a aula em vez de a
// deixar publicada sem nada para tocar.
func TestVideoFalhadoDespublicaAAula(t *testing.T) {
	r, pool, ctx := aulasCarregadas(t)
	aulaPorProcessar(t, pool, ctx, "aula-falhada", 1)
	if _, err := pool.Exec(ctx,
		`UPDATE workout_class SET published = true, mux_playback_id = 'playback-velho'
		   WHERE id = 'aula-falhada'`); err != nil {
		t.Fatal(err)
	}

	if _, err := r.MuxFalhou(ctx, "aula-falhada"); err != nil {
		t.Fatal(err)
	}

	var publicada bool
	if err := pool.QueryRow(ctx,
		`SELECT published FROM workout_class WHERE id = 'aula-falhada'`).Scan(&publicada); err != nil {
		t.Fatal(err)
	}
	if publicada {
		t.Error("uma aula cujo vídeo falhou não pode ficar publicada")
	}
}

// Recarregar o catálogo não apaga o que o webhook escreveu: as aulas do seed
// não trazem identificador, e um `UPDATE` cego punha a coluna a NULL.
func TestSeedNaoApagaOQueOWebhookEscreveu(t *testing.T) {
	r, pool, ctx := aulasCarregadas(t)

	if _, err := pool.Exec(ctx,
		`UPDATE workout_class SET mux_asset_id = 'asset-9', mux_playback_id = 'playback-9',
		                          mux_ready = true
		   WHERE id = (SELECT id FROM workout_class ORDER BY id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Seed(ctx); err != nil {
		t.Fatal(err)
	}

	var playback string
	var pronto bool
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(mux_playback_id,''), mux_ready FROM workout_class
		  WHERE mux_asset_id = 'asset-9'`).Scan(&playback, &pronto); err != nil {
		t.Fatal(err)
	}
	if playback != "playback-9" || !pronto {
		t.Errorf("o seed apagou o que o webhook escreveu: playback=%q pronto=%v", playback, pronto)
	}
}
