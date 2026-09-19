package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/airosp/airo-api/internal/engine/training"
)

// ClassRepo lê as aulas gravadas, e carrega-as.
//
// As aulas entram por seed — quem as produz é quem as põe cá dentro, e um ecrã
// de administração é uma decisão maior do que esta. O catálogo vive no JSON
// embutido no motor, como o dos exercícios: duas listas, uma no código e outra
// na base de dados, seriam duas verdades sobre o que existe.
type ClassRepo struct{ tx *TxManager }

func NewClassRepo(tx *TxManager) *ClassRepo { return &ClassRepo{tx: tx} }

type ClassRow struct {
	ID              string
	Title           string
	Specialist      string
	Focus           string
	Level           string
	DurationSeconds int
	Kcal            int
	VideoURL        string
	ThumbnailURL    string
	// Zero quer dizer "não medido" — ver a migração 0015.
	Width     int
	Height    int
	Summary   string
	Muscles   []string
	Equipment []string

	/*
	 * O vídeo no Mux.
	 *
	 * ⚠️ `MuxPlaybackID` vazio quer dizer uma aula do tempo em que um vídeo era
	 * um endereço de ficheiro. Enquanto houver dessas, `VideoURL` é o recurso —
	 * e é por isso que o `CHECK (class_tem_video)` aceita uma das duas e não
	 * exige as duas.
	 */
	MuxPlaybackID string
	MuxAssetID    string
	// MuxSigned decide se o endereço leva token. Vem da política com que o
	// recurso foi criado no Mux: servi-la ao contrário dá 403.
	MuxSigned bool
	// MuxReady é falso enquanto o Mux ainda está a processar. A aula existe,
	// tem ficha, e ainda não tem vídeo.
	MuxReady bool
}

type ClassFilter struct {
	Focus string
	Level string
	// Specialist filtra pelo treinador — é o que permite "as aulas da Ana".
	Specialist string
	// Equipment: só as aulas que a pessoa consegue fazer com o que tem.
	// Vazio na aula quer dizer "só o corpo", e essa serve sempre.
	Equipment []string
}

// Published devolve as aulas publicadas que servem o filtro.
func (r *ClassRepo) Published(ctx context.Context, f ClassFilter) ([]ClassRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, title, specialist, focus, level, duration_seconds, kcal,
		        video_url, COALESCE(thumbnail_url,''), summary, muscles, equipment,
		        COALESCE(video_width,0), COALESCE(video_height,0),
		        COALESCE(mux_playback_id,''), COALESCE(mux_asset_id,''),
		        mux_policy = 'signed', mux_ready
		   FROM workout_class
		  WHERE published
		    -- Só entram as aulas que têm mesmo o que tocar: ou um vídeo do Mux
		    -- já processado, ou o endereço directo das antigas. Uma aula
		    -- publicada enquanto o Mux ainda processa — um vídeo a ser
		    -- substituído — abre um leitor que gira para sempre, e quem o vê
		    -- conclui que a app está partida.
		    AND (mux_ready OR video_url <> '')
		    AND ($1 = '' OR focus::text = $1)
		    AND ($2 = '' OR level::text = $2)
		    AND ($3 = '' OR specialist = $3)
		    -- Uma aula sem equipamento faz-se com o corpo e entra sempre; as
		    -- outras só quando a pessoa tem **tudo** o que elas pedem.
		    --
		    -- O IS NULL não é defensivo a mais: uma lista nil chega cá como
		    -- NULL, e cardinality(NULL) = 0 é NULL — não é falso, é
		    -- desconhecido, e o AND inteiro deixa de passar. Sem esta linha,
		    -- pedir todas as aulas devolvia só as que se fazem sem nada.
		    AND ($4::text[] IS NULL
		         OR cardinality($4::text[]) = 0
		         OR cardinality(equipment) = 0
		         OR equipment <@ $4::text[])
		  -- O id desempata: as aulas entram todas no mesmo seed e, com
		  -- carimbos iguais ao segundo, a lista trocava de ordem entre pedidos.
		  ORDER BY created_at DESC, id`,
		f.Focus, f.Level, f.Specialist, f.Equipment)
	if err != nil {
		return nil, fmt.Errorf("ler aulas: %w", err)
	}
	defer rows.Close()

	out := []ClassRow{}
	for rows.Next() {
		var c ClassRow
		if err := rows.Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
			&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
			&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height,
			&c.MuxPlaybackID, &c.MuxAssetID, &c.MuxSigned, &c.MuxReady); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get devolve uma aula publicada.
func (r *ClassRepo) Get(ctx context.Context, id string) (ClassRow, error) {
	var c ClassRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id, title, specialist, focus, level, duration_seconds, kcal,
		        video_url, COALESCE(thumbnail_url,''), summary, muscles, equipment,
		        COALESCE(video_width,0), COALESCE(video_height,0),
		        COALESCE(mux_playback_id,''), COALESCE(mux_asset_id,''),
		        mux_policy = 'signed', mux_ready
		   FROM workout_class WHERE id = $1 AND published`, id,
	).Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
		&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
		&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height,
		&c.MuxPlaybackID, &c.MuxAssetID, &c.MuxSigned, &c.MuxReady)
	if errors.Is(err, pgx.ErrNoRows) {
		// Uma aula por publicar responde o mesmo que uma que não existe: quem
		// adivinhar um identificador não fica a saber que ela está a caminho.
		return ClassRow{}, ErrNotFound
	}
	return c, err
}

// ForDay escolhe a aula do dia, ou nada.
//
// A escolha é **determinística pelo dia**, como tudo o resto: o mesmo dia dá a
// mesma aula em dois telemóveis, e amanhã dá outra. Uma escolha aleatória fazia
// o treino de hoje mudar a cada abertura do ecrã.
//
// O nível é **até** ao da pessoa, não igual: uma aula de iniciante serve um
// intermédio — é mais fácil, não é impossível. Ao contrário não serve.
func (r *ClassRepo) ForDay(ctx context.Context, focus, level string, equipment []string, seed int) (ClassRow, error) {
	ordem := map[string]int{"beginner": 1, "intermediate": 2, "advanced": 3}
	tecto, ok := ordem[level]
	if !ok {
		tecto = 1
	}

	var c ClassRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`WITH servem AS (
		   SELECT id, title, specialist, focus, level, duration_seconds, kcal,
		          video_url, COALESCE(thumbnail_url,'') AS thumb, summary, muscles, equipment,
		          COALESCE(video_width,0) AS w, COALESCE(video_height,0) AS h,
		          COALESCE(mux_playback_id,'') AS playback, COALESCE(mux_asset_id,'') AS asset,
		          mux_policy = 'signed' AS assinado, mux_ready AS pronto,
		          row_number() OVER (ORDER BY id) - 1 AS n,
		          count(*) OVER () AS total
		     FROM workout_class
		    WHERE published
		      -- Como na lista: sem vídeo que toque, a aula não é o dia.
		      AND (mux_ready OR video_url <> '')
		      AND focus::text = $1
		      AND CASE level::text WHEN 'beginner' THEN 1
		                           WHEN 'intermediate' THEN 2
		                           ELSE 3 END <= $2
		      AND ($3::text[] IS NULL
		           OR cardinality(equipment) = 0
		           OR equipment <@ $3::text[])
		 )
		 SELECT id, title, specialist, focus, level, duration_seconds, kcal,
		        video_url, thumb, summary, muscles, equipment, w, h,
		        playback, asset, assinado, pronto
		   FROM servem
		  WHERE n = $4 % total`,
		focus, tecto, equipment, seed,
	).Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
		&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
		&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height,
		&c.MuxPlaybackID, &c.MuxAssetID, &c.MuxSigned, &c.MuxReady)
	if errors.Is(err, pgx.ErrNoRows) {
		// Sem aula que sirva, o dia é o plano. É a salvaguarda que impede a
		// Airo de marcar um dia de aula que não tem como encher.
		return ClassRow{}, ErrNotFound
	}
	return c, err
}

// Seed carrega as aulas do JSON embutido.
//
// Idempotente pelo `id`: correr outra vez actualiza o conteúdo, não duplica.
//
// ⚠️ **Não mexe em `published`.** Publicar é uma decisão de quem produz, não do
// deploy: uma aula tirada do ar à mão voltaria sozinha no arranque seguinte. As
// novas entram publicadas; as que já cá estavam ficam como estavam.
func (r *ClassRepo) Seed(ctx context.Context) (int, error) {
	aulas, err := training.Classes()
	if err != nil {
		return 0, err
	}
	q := r.tx.Q(ctx)

	for _, c := range aulas {
		// `[]string(nil)` chega ao Postgres como NULL, e as colunas são NOT
		// NULL com omissão `{}` — a omissão não se aplica a um NULL explícito.
		musculos, equipamento := c.Muscles, c.Equipment
		if musculos == nil {
			musculos = []string{}
		}
		if equipamento == nil {
			equipamento = []string{}
		}

		var thumb any
		if c.ThumbnailURL != "" {
			thumb = c.ThumbnailURL
		}

		/*
		 * O Mux, quando a aula já lá está.
		 *
		 * Uma aula com identificador de reprodução entra **pronta**: o seed só
		 * corre com um catálogo escrito à mão, e quem escreveu o identificador
		 * já viu o vídeo processado. As que vierem por upload nascem por
		 * processar e é o webhook que as acorda.
		 */
		var playback, asset any
		if c.MuxPlaybackID != "" {
			playback = c.MuxPlaybackID
		}
		if c.MuxAssetID != "" {
			asset = c.MuxAssetID
		}
		politica := c.MuxPolicy
		if politica != "public" {
			politica = "signed"
		}
		pronto := c.MuxPlaybackID != ""

		_, err := q.Exec(ctx,
			`INSERT INTO workout_class (id, title, specialist, focus, level,
			                            duration_seconds, kcal, video_url, thumbnail_url,
			                            summary, muscles, equipment, video_width, video_height, published,
			                            mux_playback_id, mux_asset_id, mux_policy, mux_ready)
			 VALUES ($1,$2,$3,$4::session_focus,$5::experience,$6,$7,$8,$9,$10,$11,$12,$13,$14,true,
			         $15,$16,$17,$18)
			 ON CONFLICT (id) DO UPDATE SET
			   title = EXCLUDED.title, specialist = EXCLUDED.specialist,
			   focus = EXCLUDED.focus, level = EXCLUDED.level,
			   duration_seconds = EXCLUDED.duration_seconds, kcal = EXCLUDED.kcal,
			   video_url = EXCLUDED.video_url, thumbnail_url = EXCLUDED.thumbnail_url,
			   summary = EXCLUDED.summary, muscles = EXCLUDED.muscles,
			   equipment = EXCLUDED.equipment,
			   video_width = EXCLUDED.video_width, video_height = EXCLUDED.video_height,
			   -- O que o webhook escreveu não se perde numa recarga do
			   -- catálogo: um seed sem identificador deixa o que lá está.
			   mux_playback_id = COALESCE(EXCLUDED.mux_playback_id, workout_class.mux_playback_id),
			   mux_asset_id = COALESCE(EXCLUDED.mux_asset_id, workout_class.mux_asset_id),
			   mux_policy = EXCLUDED.mux_policy,
			   mux_ready = workout_class.mux_ready OR EXCLUDED.mux_ready`,
			c.ID, c.Title, c.Specialist, string(c.Focus), string(c.Level),
			c.DurationSeconds, c.Kcal, c.VideoURL, thumb,
			c.Summary, musculos, equipamento,
			// Zero vai como NULL: a coluna guarda medidas, não palpites.
			nuloSeZero(c.Width), nuloSeZero(c.Height),
			playback, asset, politica, pronto)
		if err != nil {
			return 0, fmt.Errorf("carregar aula %q: %w", c.ID, err)
		}
	}
	return len(aulas), nil
}

// nuloSeZero manda `NULL` em vez de `0`.
//
// A coluna das dimensões guarda medidas. Um zero seria um vídeo sem altura, o
// que não existe — e o `CHECK` recusa-o, com razão. "Não medido" diz-se com
// ausência, e é isso que o cliente lê para voltar a descobrir ao carregar.
func nuloSeZero(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}

/*
 * MuxPronto marca uma aula como reproduzível.
 *
 * Chamado pelo webhook quando o Mux acaba de processar. Procura a aula pelo
 * `passthrough` — o id que lhe demos ao criar o recurso — e só por ele: casar
 * pelo identificador do recurso obrigaria a tê-lo escrito antes de ele
 * existir, que é precisamente o que não se pode fazer num upload.
 *
 * ⚠️ **A duração não se toca.** É ela que decide se o treino contou (ver
 * D15), é de quem produziu a aula, e o número medido pelo Mux é outra coisa:
 * inclui a contagem inicial e a despedida, que a aula de propósito não conta
 * como treino. Escrevê-lo por cima mudava a adesão de quem já a tinha feito.
 * O esquema também não tem como guardar "ainda não sei" — `duration_seconds`
 * é `> 0` desde a migração 13 —, por isso a ficha traz sempre um número, e é
 * esse que vale. O medido fica no registo, para quando os dois discordarem
 * de mais.
 */
func (r *ClassRepo) MuxPronto(
	ctx context.Context, aulaID, assetID, playbackID, politica string,
	largura, altura int,
) (bool, error) {
	if politica != "public" {
		politica = "signed"
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_class SET
		   mux_asset_id = $2, mux_playback_id = $3, mux_policy = $4, mux_ready = true,
		   -- As medidas, ao contrário da duração, são do ficheiro e de mais
		   -- ninguém: entram quando a ficha não as tem.
		   video_width = COALESCE(video_width, NULLIF($5, 0)),
		   video_height = COALESCE(video_height, NULLIF($6, 0))
		 WHERE id = $1`,
		aulaID, assetID, playbackID, politica, largura, altura)
	if err != nil {
		return false, fmt.Errorf("marcar aula %q como pronta: %w", aulaID, err)
	}
	return tag.RowsAffected() > 0, nil
}

/*
 * MuxFalhou desliga uma aula cujo vídeo o Mux não conseguiu processar.
 *
 * Despublica em vez de apagar: a ficha custou a escrever, o problema é do
 * ficheiro, e quem a produziu volta a enviar. Uma aula publicada com um vídeo
 * que não existe é um leitor a girar para sempre.
 */
func (r *ClassRepo) MuxFalhou(ctx context.Context, aulaID string) (bool, error) {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_class SET mux_ready = false, published = false WHERE id = $1`, aulaID)
	if err != nil {
		return false, fmt.Errorf("despublicar aula %q: %w", aulaID, err)
	}
	return tag.RowsAffected() > 0, nil
}
