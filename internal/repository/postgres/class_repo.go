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
		        COALESCE(video_width,0), COALESCE(video_height,0)
		   FROM workout_class
		  WHERE published
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
			&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height); err != nil {
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
		        COALESCE(video_width,0), COALESCE(video_height,0)
		   FROM workout_class WHERE id = $1 AND published`, id,
	).Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
		&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
		&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height)
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
		          row_number() OVER (ORDER BY id) - 1 AS n,
		          count(*) OVER () AS total
		     FROM workout_class
		    WHERE published
		      AND focus::text = $1
		      AND CASE level::text WHEN 'beginner' THEN 1
		                           WHEN 'intermediate' THEN 2
		                           ELSE 3 END <= $2
		      AND ($3::text[] IS NULL
		           OR cardinality(equipment) = 0
		           OR equipment <@ $3::text[])
		 )
		 SELECT id, title, specialist, focus, level, duration_seconds, kcal,
		        video_url, thumb, summary, muscles, equipment, w, h
		   FROM servem
		  WHERE n = $4 % total`,
		focus, tecto, equipment, seed,
	).Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
		&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
		&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height)
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

		_, err := q.Exec(ctx,
			`INSERT INTO workout_class (id, title, specialist, focus, level,
			                            duration_seconds, kcal, video_url, thumbnail_url,
			                            summary, muscles, equipment, video_width, video_height, published)
			 VALUES ($1,$2,$3,$4::session_focus,$5::experience,$6,$7,$8,$9,$10,$11,$12,$13,$14,true)
			 ON CONFLICT (id) DO UPDATE SET
			   title = EXCLUDED.title, specialist = EXCLUDED.specialist,
			   focus = EXCLUDED.focus, level = EXCLUDED.level,
			   duration_seconds = EXCLUDED.duration_seconds, kcal = EXCLUDED.kcal,
			   video_url = EXCLUDED.video_url, thumbnail_url = EXCLUDED.thumbnail_url,
			   summary = EXCLUDED.summary, muscles = EXCLUDED.muscles,
			   equipment = EXCLUDED.equipment,
			   video_width = EXCLUDED.video_width, video_height = EXCLUDED.video_height`,
			c.ID, c.Title, c.Specialist, string(c.Focus), string(c.Level),
			c.DurationSeconds, c.Kcal, c.VideoURL, thumb,
			c.Summary, musculos, equipamento,
			// Zero vai como NULL: a coluna guarda medidas, não palpites.
			nuloSeZero(c.Width), nuloSeZero(c.Height))
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
