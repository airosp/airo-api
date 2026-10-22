package painel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/admin"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

// ── Especialistas ────────────────────────────────────────────────────────────

type specialistIn struct {
	Name           string   `json:"name"`
	Role           string   `json:"role"`
	Headline       string   `json:"headline"`
	Bio            string   `json:"bio"`
	Tags           []string `json:"tags"`
	RecommendedFor []string `json:"recommendedFor"`
	ResponseTime   string   `json:"responseTime"`
	Active         *bool    `json:"active"`
	MaxClients     *int     `json:"maxClients"`
	// Ignorados: vêm de volta no corpo de quem editou o que leu.
	ID          string `json:"id"`
	Initials    string `json:"initials"`
	PhotoURL    any    `json:"photoUrl"`
	Clients     any    `json:"clients"`
	Classes     any    `json:"classes"`
	PanelAccess any    `json:"panelAccess"`
}

func viewSpecialist(s repo.AdminSpecialistRow) map[string]any {
	var access any
	if s.MemberID != nil {
		status := "invited"
		if s.MemberActive != nil && *s.MemberActive {
			status = "active"
		}
		access = map[string]any{"memberId": *s.MemberID, "email": s.MemberEmail, "status": status}
	}
	return map[string]any{
		"id": s.ID, "name": s.Name, "role": s.Role, "headline": s.Headline, "bio": s.Bio,
		"tags": s.Tags, "recommendedFor": s.RecommendedFor, "responseTime": s.ResponseTime,
		"initials": s.Initials, "photoUrl": s.PhotoURL, "active": s.Active, "clients": s.Clients,
		"classes": s.Classes, "maxClients": s.MaxClients, "panelAccess": access,
	}
}

func (a *API) SpecialistsList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Content.Specialists(r.Context(), "")
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler os especialistas.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, s := range l {
		out = append(out, viewSpecialist(s))
	}
	ok(w, map[string]any{"items": out})
}

func (a *API) saveSpecialist(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in specialistIn
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	in.Name, in.Headline = strings.TrimSpace(in.Name), strings.TrimSpace(in.Headline)
	if in.Name == "" {
		invalid(w, "O nome é obrigatório.", "name")
		return
	}
	if !oneOf(in.Role, "trainer", "nutritionist", "physio", "coach") {
		invalid(w, "Função desconhecida.", "role")
		return
	}
	if in.Headline == "" {
		invalid(w, "O título é obrigatório — é o que aparece no cartão.", "headline")
		return
	}
	if in.MaxClients != nil && *in.MaxClients <= 0 {
		in.MaxClients = nil
	}
	if create {
		id = slugOf(in.Name)
		if id == "" {
			invalid(w, "O nome precisa de letras.", "name")
			return
		}
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	row := repo.AdminSpecialistRow{
		ID: id, Name: in.Name, Role: in.Role, Headline: in.Headline, Bio: strings.TrimSpace(in.Bio),
		Tags: clean(in.Tags), RecommendedFor: clean(in.RecommendedFor), ResponseTime: strings.TrimSpace(in.ResponseTime),
		Initials: initialsOf(in.Name), Active: active, MaxClients: in.MaxClients,
	}
	if err := a.Content.SaveSpecialist(r.Context(), row, create); err != nil {
		writeRepoErr(w, r, err, "o especialista")
		return
	}
	a.audit(r, "specialist_saved", in.Name, "")
	l, err := a.Content.Specialists(r.Context(), id)
	if err != nil || len(l) == 0 {
		apierr.WriteInternal(w, r, err, "Gravado, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, viewSpecialist(l[0]))
		return
	}
	ok(w, viewSpecialist(l[0]))
}

func (a *API) SpecialistCreate(w http.ResponseWriter, r *http.Request) {
	a.saveSpecialist(w, r, "", true)
}
func (a *API) SpecialistUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveSpecialist(w, r, r.PathValue("id"), false)
}

func (a *API) SpecialistPhoto(w http.ResponseWriter, r *http.Request) {
	if a.Photos == nil {
		apierr.Write(w, apierr.Unavailable, "As fotografias estão desligadas nesta instalação.", "")
		return
	}
	id := r.PathValue("id")
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		invalid(w, "A fotografia tem de ter menos de 8 MB.", "file")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		invalid(w, "Falta a fotografia.", "file")
		return
	}
	defer f.Close()
	img, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil {
		invalid(w, "Não foi possível ler a fotografia.", "file")
		return
	}
	ct := http.DetectContentType(img)
	if !oneOf(ct, "image/jpeg", "image/png", "image/webp") {
		invalid(w, "Usa uma fotografia JPG, PNG ou WebP.", "file")
		return
	}
	if l, err := a.Content.Specialists(r.Context(), id); err != nil || len(l) == 0 {
		apierr.Write(w, apierr.NotFound, "Especialista não encontrado.", "")
		return
	}
	up, err := a.Photos.Upload(r.Context(), img, "especialista_"+id)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível guardar a fotografia.")
		return
	}
	full, _ := a.Photos.AvatarURLs(up)
	if err := a.Content.SetSpecialistPhoto(r.Context(), id, full); err != nil {
		writeRepoErr(w, r, err, "o especialista")
		return
	}
	a.audit(r, "specialist_photo", id, "")
	ok(w, map[string]any{"photoUrl": full})
}

// SpecialistInvite dá ao especialista acesso ao painel, limitado às pessoas
// que o escolheram.
func (a *API) SpecialistInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	id := r.PathValue("id")
	l, err := a.Content.Specialists(r.Context(), id)
	if err != nil || len(l) == 0 {
		apierr.Write(w, apierr.NotFound, "Especialista não encontrado.", "")
		return
	}
	a.invite(w, r, in.Email, l[0].Name, string(admin.RoleSpecialist), &id)
}

// ── Aulas ────────────────────────────────────────────────────────────────────

type classIn struct {
	Title           string   `json:"title"`
	SpecialistID    string   `json:"specialistId"`
	Focus           string   `json:"focus"`
	Level           string   `json:"level"`
	DurationSeconds int      `json:"durationSeconds"`
	Kcal            int      `json:"kcal"`
	Summary         string   `json:"summary"`
	Muscles         []string `json:"muscles"`
	Equipment       []string `json:"equipment"`
	Policy          string   `json:"policy"`
}

func (a *API) viewClass(c repo.AdminClassRow) map[string]any {
	status := c.VideoStatus
	switch {
	case c.MuxReady && c.MuxPlaybackID != "":
		status = "ready"
	case c.MuxPlaybackID == "" && c.VideoURL != "" && status != "waiting":
		status = "legacy"
	}
	policy := "public"
	if c.MuxSigned {
		policy = "signed"
	}
	var preview, thumb any
	if c.ThumbnailURL != "" {
		thumb = c.ThumbnailURL
	}
	if status == "legacy" {
		preview = c.VideoURL
	}
	if status == "ready" && a.Video != nil {
		if p, err := a.Video.Playback(c.MuxPlaybackID, c.MuxSigned); err == nil {
			preview = p.VideoURL
			if thumb == nil {
				thumb = p.ThumbnailURL
			}
		}
	}
	var playback any
	if c.MuxPlaybackID != "" {
		playback = c.MuxPlaybackID
	}
	return map[string]any{
		"id": c.ID, "title": c.Title, "specialistId": c.Specialist, "specialistName": c.SpecialistName,
		"focus": c.Focus, "level": c.Level, "durationSeconds": c.DurationSeconds, "kcal": c.Kcal,
		"summary": c.Summary, "muscles": c.Muscles, "equipment": c.Equipment, "published": c.Published,
		"thumbnailUrl": thumb, "createdAt": c.CreatedAt.UTC().Format(time.RFC3339),
		"playlists": c.Playlists, "views": c.Views,
		"video": map[string]any{"status": status, "policy": policy, "playbackId": playback, "previewUrl": preview},
	}
}

// ownClass: um especialista só mexe nas suas aulas.
func (a *API) ownClass(w http.ResponseWriter, r *http.Request, id string) (repo.AdminClassRow, bool) {
	c, err := a.Content.Class(r.Context(), id)
	if err != nil {
		writeRepoErr(w, r, err, "a aula")
		return c, false
	}
	m, _ := memberOf(r.Context())
	if s := m.specialistScope(); s != "" && c.Specialist != s {
		apierr.Write(w, apierr.Forbidden, "Só podes mexer nas tuas aulas.", "")
		return c, false
	}
	return c, true
}

func (a *API) ClassesList(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	l, err := a.Content.Classes(r.Context(), "", m.specialistScope())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as aulas.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, c := range l {
		out = append(out, a.viewClass(c))
	}
	ok(w, map[string]any{"items": out})
}

func (a *API) ClassGet(w http.ResponseWriter, r *http.Request) {
	c, okk := a.ownClass(w, r, r.PathValue("id"))
	if !okk {
		return
	}
	ok(w, a.viewClass(c))
}

func (a *API) saveClass(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var in classIn
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	m, _ := memberOf(r.Context())
	if s := m.specialistScope(); s != "" {
		in.SpecialistID = s
	}
	in.Title = strings.TrimSpace(in.Title)
	switch {
	case in.Title == "":
		invalid(w, "O título é obrigatório.", "title")
		return
	case in.SpecialistID == "":
		invalid(w, "Escolhe o especialista que dá a aula.", "specialistId")
		return
	case !oneOf(in.Focus, "upper", "lower", "cardio", "full", "mobility"):
		invalid(w, "Foco desconhecido.", "focus")
		return
	case !oneOf(in.Level, "beginner", "intermediate", "advanced"):
		invalid(w, "Nível desconhecido.", "level")
		return
	case in.DurationSeconds < 60 || in.DurationSeconds > 4*3600:
		invalid(w, "A duração tem de estar entre 1 minuto e 4 horas.", "durationSeconds")
		return
	case in.Kcal < 0 || in.Kcal > 3000:
		invalid(w, "Calorias fora do intervalo.", "kcal")
		return
	}
	if create {
		id = slugOf(in.Title)
		if id == "" {
			invalid(w, "O título precisa de letras.", "title")
			return
		}
		if _, err := a.Content.Class(r.Context(), id); err == nil {
			id += "_" + strings.ToLower(time.Now().Format("0102150405"))
		}
	} else if _, okk := a.ownClass(w, r, id); !okk {
		return
	}
	row := repo.AdminClassRow{}
	row.ID, row.Title, row.Specialist, row.Focus, row.Level = id, in.Title, in.SpecialistID, in.Focus, in.Level
	row.DurationSeconds, row.Kcal, row.Summary = in.DurationSeconds, in.Kcal, strings.TrimSpace(in.Summary)
	row.Muscles, row.Equipment, row.MuxSigned = clean(in.Muscles), clean(in.Equipment), in.Policy != "public"
	if err := a.Content.SaveClass(r.Context(), row, create); err != nil {
		if strings.Contains(err.Error(), "workout_class_specialist_fk") {
			invalid(w, "Especialista desconhecido.", "specialistId")
			return
		}
		writeRepoErr(w, r, err, "a aula")
		return
	}
	a.audit(r, "class_saved", in.Title, "")
	c, err := a.Content.Class(r.Context(), id)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Gravada, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, a.viewClass(c))
		return
	}
	ok(w, a.viewClass(c))
}

func (a *API) ClassCreate(w http.ResponseWriter, r *http.Request) { a.saveClass(w, r, "", true) }
func (a *API) ClassUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveClass(w, r, r.PathValue("id"), false)
}

// ClassUpload pede ao Mux um destino de envio. O ficheiro vai do browser
// directamente para o Mux — não passa por aqui, nem ocupa a API durante
// minutos com um vídeo de um giga.
func (a *API) ClassUpload(w http.ResponseWriter, r *http.Request) {
	if a.Video == nil {
		apierr.Write(w, apierr.Unavailable, "O envio de vídeos não está configurado nesta instalação.", "")
		return
	}
	var in struct {
		Policy string `json:"policy"`
	}
	_ = decode(r, &in)
	id := r.PathValue("id")
	c, okk := a.ownClass(w, r, id)
	if !okk {
		return
	}
	signed := in.Policy != "public"
	if signed && !a.Video.Assina() {
		apierr.Write(w, apierr.Unavailable, "Falta a chave de assinatura do Mux; envia como pública ou configura a chave.", "policy")
		return
	}
	env, err := a.Video.CriarEnvio(r.Context(), id, strings.TrimRight(a.PanelURL, "/"), signed)
	if err != nil {
		apierr.WriteInternal(w, r, err, "O Mux não aceitou o pedido de envio.")
		return
	}
	if err := a.Content.StartUpload(r.Context(), id, signed); err != nil {
		writeRepoErr(w, r, err, "a aula")
		return
	}
	// O vídeo anterior deixa de se servir: apaga-se no Mux para não custar
	// armazenamento todos os meses.
	if c.MuxAssetID != "" {
		go func(asset string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = a.Video.Apagar(ctx, asset)
		}(c.MuxAssetID)
	}
	a.audit(r, "class_upload", c.Title, "")
	ok(w, map[string]any{"uploadUrl": env.URL, "uploadId": env.ID})
}

func (a *API) ClassPublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Published bool `json:"published"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	id := r.PathValue("id")
	c, okk := a.ownClass(w, r, id)
	if !okk {
		return
	}
	if err := a.Content.Publish(r.Context(), id, in.Published); err != nil {
		if errors.Is(err, repo.ErrSemVideo) {
			apierr.Write(w, apierr.VideoNotReady, "Só se publica com o vídeo pronto.", "")
			return
		}
		writeRepoErr(w, r, err, "a aula")
		return
	}
	act := "class_unpublished"
	if in.Published {
		act = "class_published"
	}
	a.audit(r, act, c.Title, "")
	c, _ = a.Content.Class(r.Context(), id)
	ok(w, a.viewClass(c))
}

func (a *API) ClassDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, okk := a.ownClass(w, r, id)
	if !okk {
		return
	}
	if c.Playlists > 0 {
		apierr.Write(w, apierr.InPlaylist, "Esta aula está numa playlist. Tira-a de lá primeiro.", "")
		return
	}
	asset, err := a.Content.DeleteClass(r.Context(), id)
	if errors.Is(err, repo.ErrEmUso) {
		apierr.Write(w, apierr.InPlaylist, "Esta aula está numa playlist. Tira-a de lá primeiro.", "")
		return
	}
	if err != nil {
		writeRepoErr(w, r, err, "a aula")
		return
	}
	if asset != "" && a.Video != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = a.Video.Apagar(ctx, asset)
		}()
	}
	a.audit(r, "class_deleted", c.Title, "")
	w.WriteHeader(http.StatusNoContent)
}

// ── Playlists ────────────────────────────────────────────────────────────────

func (a *API) viewPlaylist(p repo.AdminPlaylistRow) map[string]any {
	items := []map[string]any{}
	total := 0
	for _, it := range p.Items {
		var thumb any
		if it.Class.ThumbnailURL != "" {
			thumb = it.Class.ThumbnailURL
		}
		total += it.Class.DurationSeconds
		items = append(items, map[string]any{
			"position": it.Position, "classId": it.Class.ID, "title": it.Class.Title, "subtitle": it.Subtitle,
			"durationSeconds": it.Class.DurationSeconds, "focus": it.Class.Focus, "thumbnailUrl": thumb,
		})
	}
	var cover any
	if p.CoverURL != "" {
		cover = p.CoverURL
	}
	return map[string]any{
		"id": p.ID, "title": p.Title, "summary": p.Summary, "coverUrl": cover, "goals": p.Goals,
		"zones": p.Zones, "level": p.Level, "published": p.Published, "items": items,
		"totalSeconds": total, "followers": p.Followers,
	}
}

func (a *API) PlaylistsList(w http.ResponseWriter, r *http.Request) {
	l, err := a.Content.Playlists(r.Context(), "")
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as playlists.")
		return
	}
	out := make([]map[string]any, 0, len(l))
	for _, p := range l {
		out = append(out, a.viewPlaylist(p))
	}
	ok(w, map[string]any{"items": out})
}

func (a *API) savePlaylist(w http.ResponseWriter, r *http.Request, id string, create bool) {
	m, _ := memberOf(r.Context())
	if m.role() == admin.RoleSpecialist {
		apierr.Write(w, apierr.Forbidden, "As playlists são da equipa de conteúdo.", "")
		return
	}
	var in struct {
		Title     string   `json:"title"`
		Summary   string   `json:"summary"`
		CoverURL  *string  `json:"coverUrl"`
		Goals     []string `json:"goals"`
		Zones     []string `json:"zones"`
		Level     string   `json:"level"`
		Published *bool    `json:"published"`
		Items     *[]struct {
			ClassID  string `json:"classId"`
			Subtitle string `json:"subtitle"`
		} `json:"items"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		invalid(w, "O título é obrigatório.", "title")
		return
	}
	if !oneOf(in.Level, "beginner", "intermediate", "advanced") {
		invalid(w, "Nível desconhecido.", "level")
		return
	}
	if create {
		id = slugOf(in.Title)
		if id == "" {
			invalid(w, "O título precisa de letras.", "title")
			return
		}
	}
	p := repo.AdminPlaylistRow{}
	p.ID, p.Title, p.Summary, p.Level = id, in.Title, strings.TrimSpace(in.Summary), in.Level
	p.Goals, p.Zones = clean(in.Goals), clean(in.Zones)
	if in.CoverURL != nil {
		p.CoverURL = strings.TrimSpace(*in.CoverURL)
	}
	if in.Published != nil {
		p.Published = *in.Published
	}
	var items []repo.PlaylistItemInput
	if in.Items != nil {
		items = []repo.PlaylistItemInput{}
		for _, it := range *in.Items {
			items = append(items, repo.PlaylistItemInput{ClassID: it.ClassID, Subtitle: strings.TrimSpace(it.Subtitle)})
		}
	}
	if p.Published && in.Items != nil && len(items) == 0 {
		invalid(w, "Uma playlist publicada precisa de pelo menos uma aula.", "items")
		return
	}
	if err := a.Content.SavePlaylist(r.Context(), p, items, create); err != nil {
		writeRepoErr(w, r, err, "a playlist")
		return
	}
	a.audit(r, "playlist_saved", in.Title, "")
	l, err := a.Content.Playlists(r.Context(), id)
	if err != nil || len(l) == 0 {
		apierr.WriteInternal(w, r, err, "Gravada, mas não foi possível ler de volta.")
		return
	}
	if create {
		created(w, a.viewPlaylist(l[0]))
		return
	}
	ok(w, a.viewPlaylist(l[0]))
}

func (a *API) PlaylistCreate(w http.ResponseWriter, r *http.Request) { a.savePlaylist(w, r, "", true) }
func (a *API) PlaylistUpdate(w http.ResponseWriter, r *http.Request) {
	a.savePlaylist(w, r, r.PathValue("id"), false)
}

func (a *API) PlaylistDelete(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	if m.role() == admin.RoleSpecialist {
		apierr.Write(w, apierr.Forbidden, "As playlists são da equipa de conteúdo.", "")
		return
	}
	id := r.PathValue("id")
	if err := a.Content.DeletePlaylist(r.Context(), id); err != nil {
		writeRepoErr(w, r, err, "a playlist")
		return
	}
	a.audit(r, "playlist_deleted", id, "")
	w.WriteHeader(http.StatusNoContent)
}
