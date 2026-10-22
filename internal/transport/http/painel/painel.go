// Package painel serve o painel da equipa da Airo: `/v1/admin/...`.
//
// Contas, sessões e permissões próprias — um token da app nunca abre nada aqui,
// e um token do painel nunca abre a app. As rotas estão em `routes.go`, e o
// percurso inteiro em `painel_api_test.go`.
package painel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/platform/clock"
	"github.com/airosp/airo-api/internal/platform/cloudinary"
	"github.com/airosp/airo-api/internal/platform/mux"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	ai "github.com/airosp/airo-api/internal/service/intelligence"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"golang.org/x/text/unicode/norm"
)

// SessionTTL é o dia de trabalho. Passado isso, volta-se a entrar.
const SessionTTL = 8 * time.Hour

// Pinger é a base de dados ou o Redis, para o estado dos serviços.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Snapshotter monta o retrato de progresso de uma pessoa — o mesmo do ecrã de
// progresso da app.
type Snapshotter interface {
	Snapshot(ctx context.Context, in service.SnapshotInput) (service.Snapshot, error)
}

// ProfileReader dá o que o retrato precisa do perfil.
type ProfileReader interface {
	Profile(ctx context.Context, userID string) (service.CreateGoalInput, error)
	TrainingDaysOf(ctx context.Context, userID string) ([]int, error)
}

// Services diz o que está configurado nesta instalação.
type Services struct {
	DB, Cache                     Pinger
	Mux, WhatsApp, Cloudinary, AI bool
}

type API struct {
	Log     *slog.Logger
	Clock   clock.Clock
	Team    *repo.AdminRepo
	Catalog *repo.AdminCatalogRepo
	Content *repo.AdminContentRepo
	Models  *repo.AdminModelsRepo
	Users   *repo.AdminUsersRepo
	// Mail entrega o código de entrada e o aviso a quem é adicionado.
	Mail Mailer
	// Pepper tempera o hash dos códigos, como na entrada da app.
	Pepper   []byte
	Limiter  auth.Limiter
	Progress Snapshotter
	Profiles ProfileReader
	// Video cria os envios para o Mux. Nil: as aulas gravam-se, os vídeos não.
	Video *mux.Client
	// Photos guarda as fotografias dos especialistas. Nil desliga-as.
	Photos *cloudinary.Client
	// Covers guarda as capas das playlists. Nil desliga o envio.
	Covers   *cloudinary.Client
	AIConfig ai.Config
	Services Services
	// PanelURL é a origem do painel: vai no email a quem é adicionado e é a origem
	// que o Mux aceita no envio pelo browser.
	PanelURL string
	// ReloadCatalog volta a entregar o catálogo gravado ao motor.
	ReloadCatalog func(ctx context.Context) error
}

func (a *API) now() time.Time {
	if a.Clock != nil {
		return a.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

// ── Sessão no contexto ───────────────────────────────────────────────────────

type memberKey struct{}

// Member é quem está do outro lado do pedido.
type Member struct {
	repo.MemberRow
	Token []byte
}

func memberOf(ctx context.Context) (Member, bool) {
	m, ok := ctx.Value(memberKey{}).(Member)
	return m, ok
}

func (m Member) role() admin.Role { return admin.Role(m.Role) }

// specialistScope é o especialista a que um membro está limitado, ou vazio.
func (m Member) specialistScope() string {
	if m.role() == admin.RoleSpecialist && m.SpecialistID != nil {
		return *m.SpecialistID
	}
	if m.role() == admin.RoleSpecialist {
		// Um especialista sem ficha não vê ninguém — nunca toda a gente.
		return "\x00sem-ficha"
	}
	return ""
}

// authed exige uma sessão do painel.
func (a *API) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
			return
		}
		hash := admin.HashToken(strings.TrimSpace(token))
		m, _, err := a.Team.SessionMember(r.Context(), hash, a.now())
		if err != nil {
			if !errors.Is(err, repo.ErrNotFound) {
				apierr.WriteInternal(w, r, err, "Não foi possível verificar a sessão.")
				return
			}
			apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
			return
		}
		a.Team.TouchMember(r.Context(), m.ID, a.now())
		// Respostas do painel têm dados de pessoas: nunca em cache.
		w.Header().Set("Cache-Control", "no-store")
		next(w, r.WithContext(context.WithValue(r.Context(), memberKey{}, Member{MemberRow: m, Token: hash})))
	}
}

// can exige o nível na área, depois da sessão.
func (a *API) can(area admin.Area, level admin.Level, next http.HandlerFunc) http.HandlerFunc {
	return a.authed(func(w http.ResponseWriter, r *http.Request) {
		m, _ := memberOf(r.Context())
		if !admin.Can(m.role(), area, level) {
			apierr.Write(w, apierr.Forbidden, "A tua função não tem acesso a isto.", "")
			return
		}
		next(w, r)
	})
}

// audit regista uma acção. Uma falha a registar não desfaz a acção, mas fica
// no registo do servidor — uma auditoria com buracos tem de se notar.
func (a *API) audit(r *http.Request, action, target, detail string) {
	m, ok := memberOf(r.Context())
	var id *string
	name := "Sistema"
	if ok {
		id, name = &m.ID, m.Name
	}
	if err := a.Team.Audit(context.WithoutCancel(r.Context()), id, name, action, target, detail); err != nil && a.Log != nil {
		a.Log.Error("auditoria do painel", "error", err, "accao", action)
	}
}

// ── Utilidades ───────────────────────────────────────────────────────────────

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(dst)
}

func ok(w http.ResponseWriter, body any) { apierr.WriteJSON(w, http.StatusOK, body) }

func created(w http.ResponseWriter, body any) { apierr.WriteJSON(w, http.StatusCreated, body) }

func invalid(w http.ResponseWriter, message, field string) {
	apierr.Write(w, apierr.ValidationFailed, message, field)
}

// writeRepoErr traduz os erros comuns dos repositórios.
func writeRepoErr(w http.ResponseWriter, r *http.Request, err error, what string) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		apierr.Write(w, apierr.NotFound, what+" não encontrado.", "")
	case errors.Is(err, repo.ErrDuplicate):
		apierr.Write(w, apierr.Conflict, "Já existe "+what+" com esse identificador.", "id")
	default:
		apierr.WriteInternal(w, r, err, "Não foi possível gravar "+what+".")
	}
}

var naoSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugOf faz um identificador a partir de um nome: "Carla Vieira" → carla_vieira.
func slugOf(s string) string {
	t := norm.NFD.String(strings.ToLower(s))
	b := strings.Builder{}
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Trim(naoSlug.ReplaceAllString(b.String(), "_"), "_")
	if len(out) > 48 {
		out = strings.TrimRight(out[:48], "_")
	}
	return out
}

func initialsOf(name string) string {
	parts := strings.Fields(name)
	out := ""
	for i, p := range parts {
		if i == 0 || i == len(parts)-1 {
			for _, r := range p {
				out += string(unicode.ToUpper(r))
				break
			}
		}
	}
	return out
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func clean(list []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func iso(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func day(t time.Time) string { return t.Format("2006-01-02") }
