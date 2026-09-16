package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/airosp/airo-api/internal/engine/training"
)

// PlaylistRepo lê as playlists, carrega-as, e guarda por onde cada pessoa vai.
//
// O catálogo entra por seed, como o das aulas: quem produz é quem monta a
// sequência. O que é de cada pessoa — feito, saltado, quanto viu — vive em
// `playlist_item_state` e nunca no catálogo.
type PlaylistRepo struct{ tx *TxManager }

func NewPlaylistRepo(tx *TxManager) *PlaylistRepo { return &PlaylistRepo{tx: tx} }

type PlaylistRow struct {
	ID       string
	Title    string
	Summary  string
	CoverURL string
	Goals    []string
	Zones    []string
	Level    string
}

// PlaylistItemRow é uma aula no seu lugar da lista, já com o que é preciso
// para a desenhar sem um segundo pedido.
type PlaylistItemRow struct {
	Position int
	// Subtitle é o papel da aula nesta lista: "Aquecimento · Cardio".
	Subtitle string
	Class    ClassRow
}

// PlaylistStateRow é por onde uma pessoa vai numa posição.
type PlaylistStateRow struct {
	Position       int
	Status         string
	WatchedSeconds int
}

// Published devolve as playlists publicadas.
func (r *PlaylistRepo) Published(ctx context.Context) ([]PlaylistRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, title, summary, COALESCE(cover_url,''), goals, zones, level::text
		   FROM playlist
		  WHERE published
		  ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("playlists: %w", err)
	}
	defer rows.Close()

	var out []PlaylistRow
	for rows.Next() {
		var p PlaylistRow
		if err := rows.Scan(&p.ID, &p.Title, &p.Summary, &p.CoverURL,
			&p.Goals, &p.Zones, &p.Level); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get devolve uma playlist publicada.
func (r *PlaylistRepo) Get(ctx context.Context, id string) (PlaylistRow, error) {
	var p PlaylistRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id, title, summary, COALESCE(cover_url,''), goals, zones, level::text
		   FROM playlist WHERE id = $1 AND published`, id).
		Scan(&p.ID, &p.Title, &p.Summary, &p.CoverURL, &p.Goals, &p.Zones, &p.Level)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlaylistRow{}, ErrNotFound
	}
	return p, err
}

// Items devolve as aulas da lista, por ordem, já com os dados da aula.
//
// Uma consulta só: pedir a lista e depois uma aula de cada vez era N+1 pedidos
// para desenhar um ecrã que mostra as N ao mesmo tempo.
func (r *PlaylistRepo) Items(ctx context.Context, playlistID string) ([]PlaylistItemRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT i.position, i.subtitle,
		        c.id, c.title, c.specialist, c.focus::text, c.level::text,
		        c.duration_seconds, c.kcal, c.video_url, COALESCE(c.thumbnail_url,''),
		        c.summary, c.muscles, c.equipment,
		        COALESCE(c.video_width,0), COALESCE(c.video_height,0)
		   FROM playlist_item i
		   JOIN workout_class c ON c.id = i.class_id
		  WHERE i.playlist_id = $1
		  ORDER BY i.position`, playlistID)
	if err != nil {
		return nil, fmt.Errorf("aulas da playlist: %w", err)
	}
	defer rows.Close()

	var out []PlaylistItemRow
	for rows.Next() {
		var it PlaylistItemRow
		c := &it.Class
		if err := rows.Scan(&it.Position, &it.Subtitle, &c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
			&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
			&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// States devolve por onde a pessoa vai, por posição.
func (r *PlaylistRepo) States(ctx context.Context, userID, playlistID string) (map[int]PlaylistStateRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT position, status::text, watched_seconds
		   FROM playlist_item_state
		  WHERE user_id = $1 AND playlist_id = $2`, userID, playlistID)
	if err != nil {
		return nil, fmt.Errorf("estado da playlist: %w", err)
	}
	defer rows.Close()

	out := map[int]PlaylistStateRow{}
	for rows.Next() {
		var s PlaylistStateRow
		if err := rows.Scan(&s.Position, &s.Status, &s.WatchedSeconds); err != nil {
			return nil, err
		}
		out[s.Position] = s
	}
	return out, rows.Err()
}

// SetState marca uma posição.
//
// `watched_seconds` só cresce: rever um vídeo do princípio não pode apagar que
// ele já tinha sido visto até ao fim.
func (r *PlaylistRepo) SetState(ctx context.Context, userID, playlistID string, position int, status string, watched int) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO playlist_item_state (user_id, playlist_id, position, status, watched_seconds)
		 VALUES ($1,$2,$3,$4::playlist_item_status,$5)
		 ON CONFLICT (user_id, playlist_id, position) DO UPDATE SET
		   status = EXCLUDED.status,
		   watched_seconds = GREATEST(playlist_item_state.watched_seconds, EXCLUDED.watched_seconds),
		   updated_at = now()`,
		userID, playlistID, position, status, watched)
	return err
}

// ClearStates devolve a lista ao princípio, para quem a quer repetir.
func (r *PlaylistRepo) ClearStates(ctx context.Context, userID, playlistID string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM playlist_item_state WHERE user_id = $1 AND playlist_id = $2`,
		userID, playlistID)
	return err
}

// ItemClass devolve a aula numa posição — e o não-encontrado distingue-se de um
// erro, para o transporte poder responder 404 em vez de 500.
func (r *PlaylistRepo) ItemClass(ctx context.Context, playlistID string, position int) (ClassRow, error) {
	var c ClassRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT c.id, c.title, c.specialist, c.focus::text, c.level::text,
		        c.duration_seconds, c.kcal, c.video_url, COALESCE(c.thumbnail_url,''),
		        c.summary, c.muscles, c.equipment,
		        COALESCE(c.video_width,0), COALESCE(c.video_height,0)
		   FROM playlist_item i
		   JOIN workout_class c ON c.id = i.class_id
		  WHERE i.playlist_id = $1 AND i.position = $2`, playlistID, position).
		Scan(&c.ID, &c.Title, &c.Specialist, &c.Focus, &c.Level,
			&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL,
			&c.Summary, &c.Muscles, &c.Equipment, &c.Width, &c.Height)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClassRow{}, ErrNotFound
	}
	return c, err
}

// Seed carrega as playlists do JSON embutido.
//
// Idempotente pelo `id`, como o das aulas, e **não mexe em `published`** pela
// mesma razão: publicar é decisão de quem produz, não do arranque.
//
// As posições são reescritas de uma vez: apagar e voltar a inserir é o que
// permite reordenar uma lista sem deixar restos da ordem anterior. O estado de
// quem já a começou cai com elas — é o preço de mudar a lista debaixo dos pés,
// e é por isso que reordenar uma lista publicada é uma decisão, não um detalhe.
func (r *PlaylistRepo) Seed(ctx context.Context) (int, error) {
	listas, err := training.Playlists()
	if err != nil {
		return 0, err
	}
	q := r.tx.Q(ctx)

	for _, p := range listas {
		objetivos, zonas := p.Goals, p.Zones
		if objetivos == nil {
			objetivos = []string{}
		}
		if zonas == nil {
			zonas = []string{}
		}
		var capa any
		if p.CoverURL != "" {
			capa = p.CoverURL
		}

		if _, err := q.Exec(ctx,
			`INSERT INTO playlist (id, title, summary, cover_url, goals, zones, level, published)
			 VALUES ($1,$2,$3,$4,$5,$6,$7::experience,true)
			 ON CONFLICT (id) DO UPDATE SET
			   title = EXCLUDED.title, summary = EXCLUDED.summary,
			   cover_url = EXCLUDED.cover_url, goals = EXCLUDED.goals,
			   zones = EXCLUDED.zones, level = EXCLUDED.level`,
			p.ID, p.Title, p.Summary, capa, objetivos, zonas, string(p.Level)); err != nil {
			return 0, fmt.Errorf("playlist %q: %w", p.ID, err)
		}

		if _, err := q.Exec(ctx,
			`DELETE FROM playlist_item WHERE playlist_id = $1 AND position > $2`,
			p.ID, len(p.Items)); err != nil {
			return 0, fmt.Errorf("playlist %q: limpar posições: %w", p.ID, err)
		}
		for i, item := range p.Items {
			if _, err := q.Exec(ctx,
				`INSERT INTO playlist_item (playlist_id, position, class_id, subtitle)
				 VALUES ($1,$2,$3,$4)
				 ON CONFLICT (playlist_id, position) DO UPDATE SET
				   class_id = EXCLUDED.class_id, subtitle = EXCLUDED.subtitle`,
				p.ID, i+1, item.Class, item.Subtitle); err != nil {
				return 0, fmt.Errorf("playlist %q posição %d: %w", p.ID, i+1, err)
			}
		}
	}
	return len(listas), nil
}
