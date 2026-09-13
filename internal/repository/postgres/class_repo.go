package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ClassRepo lê as aulas gravadas.
//
// Só lê. As aulas entram por seed — quem as produz é quem as põe cá dentro, e
// um ecrã de administração é uma decisão maior do que esta.
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
	Summary         string
	Muscles         []string
	Equipment       []string
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
		        video_url, COALESCE(thumbnail_url,''), summary, muscles, equipment
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
		  ORDER BY created_at DESC`,
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
			&c.Summary, &c.Muscles, &c.Equipment); err != nil {
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
		        video_url, COALESCE(thumbnail_url,''), summary, muscles, equipment
		   FROM workout_class WHERE id = $1 AND published`, id,
	).Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
		&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
		&c.Summary, &c.Muscles, &c.Equipment)
	if errors.Is(err, pgx.ErrNoRows) {
		// Uma aula por publicar responde o mesmo que uma que não existe: quem
		// adivinhar um identificador não fica a saber que ela está a caminho.
		return ClassRow{}, ErrNotFound
	}
	return c, err
}
