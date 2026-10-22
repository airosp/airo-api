package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminContentRepo é o conteúdo que a equipa publica: especialistas, aulas e
// playlists. Ao contrário dos exercícios, a app já lê estes da base de dados —
// gravar aqui é publicar.
type AdminContentRepo struct{ tx *TxManager }

func NewAdminContentRepo(tx *TxManager) *AdminContentRepo { return &AdminContentRepo{tx: tx} }

// ── Especialistas ────────────────────────────────────────────────────────────

type AdminSpecialistRow struct {
	ID             string
	Name           string
	Role           string
	Headline       string
	Bio            string
	Tags           []string
	RecommendedFor []string
	ResponseTime   string
	Initials       string
	PhotoURL       *string
	Active         bool
	MaxClients     *int
	Clients        int
	Classes        int
	MemberID       *string
	MemberEmail    *string
	MemberActive   *bool
}

func (r *AdminContentRepo) Specialists(ctx context.Context, id string) ([]AdminSpecialistRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT s.id, s.name, s.role::text, s.headline, s.bio, s.tags, s.recommended_for, s.response_time,
		        s.initials, s.photo_url, s.active, s.max_clients,
		        (SELECT count(*) FROM user_specialist u JOIN app_user a ON a.id = u.user_id
		          WHERE u.specialist_id = s.id AND a.deleted_at IS NULL),
		        (SELECT count(*) FROM workout_class c WHERE c.specialist = s.id),
		        m.id::text, m.email::text, m.last_seen_at IS NOT NULL
		   FROM specialist s
		   LEFT JOIN LATERAL (SELECT * FROM admin_member m WHERE m.specialist_id = s.id AND m.active
		                       ORDER BY m.created_at DESC LIMIT 1) m ON true
		  WHERE ($1 = '' OR s.id = $1)
		  ORDER BY s.active DESC, s.role, s.name`, id)
	if err != nil {
		return nil, fmt.Errorf("ler especialistas: %w", err)
	}
	defer rows.Close()
	out := []AdminSpecialistRow{}
	for rows.Next() {
		var s AdminSpecialistRow
		if err := rows.Scan(&s.ID, &s.Name, &s.Role, &s.Headline, &s.Bio, &s.Tags, &s.RecommendedFor,
			&s.ResponseTime, &s.Initials, &s.PhotoURL, &s.Active, &s.MaxClients, &s.Clients, &s.Classes,
			&s.MemberID, &s.MemberEmail, &s.MemberActive); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *AdminContentRepo) SaveSpecialist(ctx context.Context, s AdminSpecialistRow, create bool) error {
	args := []any{s.ID, s.Name, s.Role, s.Headline, s.Bio, listaOuVazia(s.Tags),
		listaOuVazia(s.RecommendedFor), s.ResponseTime, s.Initials, s.Active, s.MaxClients}
	if create {
		_, err := r.tx.Q(ctx).Exec(ctx,
			`INSERT INTO specialist (id, name, role, headline, bio, tags, recommended_for, response_time,
			   initials, active, max_clients, admin_edited_at)
			 VALUES ($1,$2,$3::specialist_role,$4,$5,$6,$7,$8,$9,$10,$11,now())`, args...)
		if uniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE specialist SET name=$2, role=$3::specialist_role, headline=$4, bio=$5, tags=$6,
		   recommended_for=$7, response_time=$8, initials=$9, active=$10, max_clients=$11,
		   admin_edited_at=now()
		 WHERE id=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *AdminContentRepo) SetSpecialistPhoto(ctx context.Context, id, url string) error {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE specialist SET photo_url = $2, admin_edited_at = now() WHERE id = $1`, id, url)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Aulas ────────────────────────────────────────────────────────────────────

type AdminClassRow struct {
	ClassRow
	SpecialistName string
	Published      bool
	VideoStatus    string
	CreatedAt      time.Time
	Playlists      int
	Views          int
}

func (r *AdminContentRepo) Classes(ctx context.Context, id, specialist string) ([]AdminClassRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT c.id, c.title, c.specialist, coalesce(s.name, c.specialist), c.focus::text, c.level::text,
		        c.duration_seconds, c.kcal, c.video_url, coalesce(c.thumbnail_url, ''),
		        coalesce(c.video_width, 0), coalesce(c.video_height, 0), c.summary, c.muscles, c.equipment,
		        coalesce(c.mux_playback_id, ''), coalesce(c.mux_asset_id, ''), c.mux_policy = 'signed',
		        c.mux_ready, c.published, c.video_status, c.created_at,
		        (SELECT count(DISTINCT playlist_id) FROM playlist_item p WHERE p.class_id = c.id),
		        (SELECT count(*) FROM workout_session w WHERE w.class_id = c.id)
		   FROM workout_class c LEFT JOIN specialist s ON s.id = c.specialist
		  WHERE ($1 = '' OR c.id = $1) AND ($2 = '' OR c.specialist = $2)
		  ORDER BY c.created_at DESC, c.title`, id, specialist)
	if err != nil {
		return nil, fmt.Errorf("ler aulas: %w", err)
	}
	defer rows.Close()
	out := []AdminClassRow{}
	for rows.Next() {
		var c AdminClassRow
		if err := rows.Scan(&c.ID, &c.Title, &c.Specialist, &c.SpecialistName, &c.Focus, &c.Level,
			&c.DurationSeconds, &c.Kcal, &c.VideoURL, &c.ThumbnailURL, &c.Width, &c.Height, &c.Summary,
			&c.Muscles, &c.Equipment, &c.MuxPlaybackID, &c.MuxAssetID, &c.MuxSigned, &c.MuxReady,
			&c.Published, &c.VideoStatus, &c.CreatedAt, &c.Playlists, &c.Views); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *AdminContentRepo) Class(ctx context.Context, id string) (AdminClassRow, error) {
	l, err := r.Classes(ctx, id, "")
	if err != nil {
		return AdminClassRow{}, err
	}
	if len(l) == 0 {
		return AdminClassRow{}, ErrNotFound
	}
	return l[0], nil
}

func (r *AdminContentRepo) SaveClass(ctx context.Context, c AdminClassRow, create bool) error {
	politica := "public"
	if c.MuxSigned {
		politica = "signed"
	}
	args := []any{c.ID, c.Title, c.Specialist, c.Focus, c.Level, c.DurationSeconds, c.Kcal, c.Summary,
		listaOuVazia(c.Muscles), listaOuVazia(c.Equipment), politica}
	if create {
		_, err := r.tx.Q(ctx).Exec(ctx,
			`INSERT INTO workout_class (id, title, specialist, focus, level, duration_seconds, kcal, summary,
			   muscles, equipment, mux_policy, published, admin_edited_at)
			 VALUES ($1,$2,$3,$4::session_focus,$5::experience,$6,$7,$8,$9,$10,$11,false,now())`, args...)
		if uniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	// A política só muda enquanto não há vídeo: a do recurso no Mux já está
	// decidida, e servi-lo ao contrário dá 403.
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_class SET title=$2, specialist=$3, focus=$4::session_focus, level=$5::experience,
		   duration_seconds=$6, kcal=$7, summary=$8, muscles=$9, equipment=$10,
		   mux_policy = CASE WHEN mux_playback_id IS NULL THEN $11 ELSE mux_policy END,
		   admin_edited_at=now()
		 WHERE id=$1`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// StartUpload marca a aula à espera do vídeo. Um vídeo novo substitui o
// anterior: a aula sai do ar até o novo estar pronto.
func (r *AdminContentRepo) StartUpload(ctx context.Context, id string, signed bool) error {
	politica := "public"
	if signed {
		politica = "signed"
	}
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_class SET video_status='waiting', mux_ready=false, mux_playback_id=NULL,
		   mux_asset_id=NULL, mux_policy=$2, published = (published AND video_url <> ''),
		   admin_edited_at=now()
		 WHERE id=$1`, id, politica)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrSemVideo — publicar uma aula sem vídeo pronto.
var ErrSemVideo = errors.New("aula sem vídeo pronto")

func (r *AdminContentRepo) Publish(ctx context.Context, id string, published bool) error {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE workout_class SET published=$2, admin_edited_at=now()
		  WHERE id=$1 AND (NOT $2 OR (mux_ready AND mux_playback_id IS NOT NULL) OR
		                  (mux_playback_id IS NULL AND video_url <> ''))`, id, published)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := r.Class(ctx, id); err != nil {
			return err
		}
		return ErrSemVideo
	}
	return nil
}

// ErrEmUso — apagar o que ainda é usado.
var ErrEmUso = errors.New("em uso")

func (r *AdminContentRepo) DeleteClass(ctx context.Context, id string) (assetID string, err error) {
	err = r.tx.Q(ctx).QueryRow(ctx,
		`DELETE FROM workout_class WHERE id = $1 RETURNING coalesce(mux_asset_id, '')`, id).Scan(&assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil && isFKViolation(err) {
		return "", ErrEmUso
	}
	return assetID, err
}

// ── Playlists ────────────────────────────────────────────────────────────────

type AdminPlaylistRow struct {
	PlaylistRow
	Published bool
	Items     []PlaylistItemRow
	Followers int
}

func (r *AdminContentRepo) Playlists(ctx context.Context, id string) ([]AdminPlaylistRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT p.id, p.title, p.summary, coalesce(p.cover_url, ''), p.goals, p.zones, p.level::text,
		        p.published,
		        (SELECT count(DISTINCT user_id) FROM playlist_item_state s WHERE s.playlist_id = p.id)
		   FROM playlist p WHERE ($1 = '' OR p.id = $1) ORDER BY p.created_at, p.title`, id)
	if err != nil {
		return nil, fmt.Errorf("ler playlists: %w", err)
	}
	out := []AdminPlaylistRow{}
	for rows.Next() {
		var p AdminPlaylistRow
		if err := rows.Scan(&p.ID, &p.Title, &p.Summary, &p.CoverURL, &p.Goals, &p.Zones, &p.Level,
			&p.Published, &p.Followers); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := NewPlaylistRepo(r.tx)
	for i := range out {
		l, err := items.Items(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Items = l
	}
	return out, nil
}

type PlaylistItemInput struct {
	ClassID  string
	Subtitle string
}

// SavePlaylist grava a ficha e, quando vêm, as aulas por ordem. Reescrever as
// posições apaga o progresso de quem já a começou — por isso só se reescreve
// quando a ordem mudou de facto.
func (r *AdminContentRepo) SavePlaylist(ctx context.Context, p AdminPlaylistRow, items []PlaylistItemInput, create bool) error {
	return r.tx.Do(ctx, func(ctx context.Context) error {
		q := r.tx.Q(ctx)
		var capa any
		if p.CoverURL != "" {
			capa = p.CoverURL
		}
		args := []any{p.ID, p.Title, p.Summary, capa, listaOuVazia(p.Goals), listaOuVazia(p.Zones), p.Level, p.Published}
		if create {
			_, err := q.Exec(ctx,
				`INSERT INTO playlist (id, title, summary, cover_url, goals, zones, level, published, admin_edited_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7::experience,$8,now())`, args...)
			if uniqueViolation(err) {
				return ErrDuplicate
			}
			if err != nil {
				return err
			}
		} else {
			tag, err := q.Exec(ctx,
				`UPDATE playlist SET title=$2, summary=$3, cover_url=$4, goals=$5, zones=$6,
				   level=$7::experience, published=$8, admin_edited_at=now() WHERE id=$1`, args...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if items == nil {
			return nil
		}
		actuais, err := NewPlaylistRepo(r.tx).Items(ctx, p.ID)
		if err != nil {
			return err
		}
		igual := len(actuais) == len(items)
		for i := 0; igual && i < len(items); i++ {
			igual = actuais[i].Class.ID == items[i].ClassID
		}
		if igual {
			for i, it := range items {
				if _, err := q.Exec(ctx,
					`UPDATE playlist_item SET subtitle=$3 WHERE playlist_id=$1 AND position=$2`,
					p.ID, i+1, it.Subtitle); err != nil {
					return err
				}
			}
			return nil
		}
		if _, err := q.Exec(ctx, `DELETE FROM playlist_item WHERE playlist_id = $1`, p.ID); err != nil {
			return err
		}
		for i, it := range items {
			if _, err := q.Exec(ctx,
				`INSERT INTO playlist_item (playlist_id, position, class_id, subtitle) VALUES ($1,$2,$3,$4)`,
				p.ID, i+1, it.ClassID, it.Subtitle); err != nil {
				if isFKViolation(err) {
					return fmt.Errorf("%w: aula %q", ErrNotFound, it.ClassID)
				}
				return err
			}
		}
		return nil
	})
}

func (r *AdminContentRepo) DeletePlaylist(ctx context.Context, id string) error {
	tag, err := r.tx.Q(ctx).Exec(ctx, `DELETE FROM playlist WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
