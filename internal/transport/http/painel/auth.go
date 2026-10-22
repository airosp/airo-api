package painel

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/auth"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

const (
	challengeTTL = 5 * time.Minute
	lockFor      = 15 * time.Minute
	inviteTTL    = 7 * 24 * time.Hour
)

type memberView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Email        string  `json:"email"`
	Role         string  `json:"role"`
	SpecialistID *string `json:"specialistId"`
	TOTPEnabled  bool    `json:"totpEnabled"`
	Active       bool    `json:"active"`
	LastSeenAt   *string `json:"lastSeenAt"`
	CreatedAt    string  `json:"createdAt"`
	Initials     string  `json:"initials"`
	// Invited: ainda não aceitou o convite.
	Invited bool `json:"invited"`
}

func viewMember(m repo.MemberRow) memberView {
	return memberView{
		ID: m.ID, Name: m.Name, Email: m.Email, Role: m.Role, SpecialistID: m.SpecialistID,
		TOTPEnabled: m.TOTPEnabled, Active: m.Active, LastSeenAt: iso(m.LastSeenAt),
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339), Initials: initialsOf(m.Name),
		Invited: !m.TOTPEnabled,
	}
}

func (a *API) limited(r *http.Request, key string, max int) bool {
	if a.Limiter == nil {
		return false
	}
	_, err := a.Limiter.Allow(r.Context(), "painel:"+key, auth.Limit{Max: max, Window: time.Hour})
	return errors.Is(err, auth.ErrRateLimited)
}

func ipKey(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if i := strings.IndexByte(ip, ','); i > 0 {
		ip = ip[:i]
	}
	if ip == "" {
		ip = r.RemoteAddr
		if i := strings.LastIndexByte(ip, ':'); i > 0 {
			ip = ip[:i]
		}
	}
	return auth.HashIP(strings.TrimSpace(ip))
}

// Login é o primeiro passo: email e palavra-passe. A resposta a um email que
// não existe é igual à de uma palavra-passe errada, e demora o mesmo.
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	if a.limited(r, "ip:"+ipKey(r), 30) {
		apierr.Write(w, apierr.RateLimited, "Demasiadas tentativas. Tenta daqui a uma hora.", "")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	m, err := a.Team.MemberByEmail(r.Context(), email)
	if errors.Is(err, repo.ErrNotFound) || (err == nil && (!m.Active || !m.TOTPEnabled)) {
		admin.DummyCheck(in.Password)
		apierr.Write(w, apierr.Unauthorized, "Email ou palavra-passe errados.", "")
		return
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível entrar.")
		return
	}
	now := a.now()
	if m.LockedUntil != nil && m.LockedUntil.After(now) {
		apierr.Write(w, apierr.Locked, "Conta trancada depois de 5 tentativas erradas. Tenta daqui a 15 minutos.", "")
		return
	}
	if !admin.CheckPassword(m.PasswordHash, in.Password) {
		_ = a.Team.LoginFailed(r.Context(), m.ID, lockFor, now)
		a.auditAs(r, m, "login_failed", m.Email, "Palavra-passe errada")
		apierr.Write(w, apierr.Unauthorized, "Email ou palavra-passe errados.", "")
		return
	}
	token, hash, err := admin.NewToken()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível entrar.")
		return
	}
	if err := a.Team.NewChallenge(r.Context(), hash, m.ID, now.Add(challengeTTL)); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível entrar.")
		return
	}
	ok(w, map[string]any{"challenge": token, "expiresAt": now.Add(challengeTTL).Format(time.RFC3339)})
}

// Verify é o segundo passo: o código da app autenticadora.
func (a *API) Verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	now := a.now()
	hash := admin.HashToken(in.Challenge)
	memberID, attempts, err := a.Team.Challenge(r.Context(), hash, now)
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.ChallengeExpired, "O passo anterior expirou. Volta a pôr a palavra-passe.", "")
		return
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível entrar.")
		return
	}
	m, err := a.Team.MemberByID(r.Context(), memberID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível entrar.")
		return
	}
	secret, err := a.Sealer.Open(m.TOTPSecret)
	if err != nil || !m.Active || !admin.VerifyTOTP(secret, in.Code, now) {
		if attempts >= 5 {
			_ = a.Team.DropChallenge(r.Context(), hash)
			_ = a.Team.LoginFailed(r.Context(), m.ID, lockFor, now)
			a.auditAs(r, m, "login_blocked", m.Email, "5 códigos errados")
		}
		apierr.Write(w, apierr.InvalidCode, "Código errado.", "code")
		return
	}
	_ = a.Team.DropChallenge(r.Context(), hash)
	a.startSession(w, r, m, "login")
}

func (a *API) startSession(w http.ResponseWriter, r *http.Request, m repo.MemberRow, action string) {
	now := a.now()
	token, hash, err := admin.NewToken()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível abrir a sessão.")
		return
	}
	if err := a.Team.NewSession(r.Context(), hash, m.ID, now.Add(SessionTTL)); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível abrir a sessão.")
		return
	}
	_ = a.Team.LoginOK(r.Context(), m.ID, now)
	a.auditAs(r, m, action, m.Email, "")
	ok(w, map[string]any{
		"token": token, "expiresAt": now.Add(SessionTTL).Format(time.RFC3339),
		"member": viewMember(m), "permissions": admin.Permissions(admin.Role(m.Role)),
	})
}

func (a *API) auditAs(r *http.Request, m repo.MemberRow, action, target, detail string) {
	id := m.ID
	if err := a.Team.Audit(r.Context(), &id, m.Name, action, target, detail); err != nil && a.Log != nil {
		a.Log.Error("auditoria do painel", "error", err)
	}
}

func (a *API) Logout(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	_ = a.Team.RevokeSession(r.Context(), m.Token)
	a.audit(r, "logout", m.Email, "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	ok(w, map[string]any{"member": viewMember(m.MemberRow), "permissions": admin.Permissions(m.role())})
}

// ── Convites ─────────────────────────────────────────────────────────────────

func (a *API) inviteURL(token string) string {
	return strings.TrimRight(a.PanelURL, "/") + "/convite/" + token
}

// Invite mostra o convite e o segredo da app autenticadora. O segredo gera-se
// na primeira abertura e mantém-se até o código o confirmar: voltar a abrir a
// página não pode invalidar o QR que a pessoa acabou de ler.
func (a *API) Invite(w http.ResponseWriter, r *http.Request) {
	m, err := a.Team.MemberByInvite(r.Context(), admin.HashToken(r.PathValue("token")), a.now())
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Este convite já foi usado ou expirou. Pede um novo.", "")
		return
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível abrir o convite.")
		return
	}
	var secret []byte
	if len(m.TOTPSecret) > 0 {
		secret, err = a.Sealer.Open(m.TOTPSecret)
	}
	if len(secret) == 0 || err != nil {
		secret, err = admin.NewTOTPSecret()
		if err != nil {
			apierr.WriteInternal(w, r, err, "Não foi possível abrir o convite.")
			return
		}
		sealed, err := a.Sealer.Seal(secret)
		if err != nil {
			apierr.WriteInternal(w, r, err, "Não foi possível abrir o convite.")
			return
		}
		if err := a.Team.SetPendingTOTP(r.Context(), m.ID, sealed); err != nil {
			apierr.WriteInternal(w, r, err, "Não foi possível abrir o convite.")
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	ok(w, map[string]any{
		"email": m.Email, "name": m.Name, "role": m.Role,
		"otpauthUri": admin.OTPAuthURI(secret, m.Email), "secret": admin.SecretText(secret),
	})
}

func (a *API) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	if a.limited(r, "convite:"+ipKey(r), 20) {
		apierr.Write(w, apierr.RateLimited, "Demasiadas tentativas. Tenta daqui a uma hora.", "")
		return
	}
	now := a.now()
	m, err := a.Team.MemberByInvite(r.Context(), admin.HashToken(r.PathValue("token")), now)
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Este convite já foi usado ou expirou. Pede um novo.", "")
		return
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível aceitar o convite.")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = m.Name
	}
	if len(in.Password) < admin.MinPassword {
		invalid(w, "A palavra-passe precisa de pelo menos 10 caracteres.", "password")
		return
	}
	hash, err := admin.HashPassword(in.Password)
	if err != nil {
		invalid(w, "A palavra-passe tem de ter entre 10 e 72 caracteres.", "password")
		return
	}
	if len(m.TOTPSecret) == 0 {
		invalid(w, "Abre o convite outra vez para ler o código QR.", "code")
		return
	}
	secret, err := a.Sealer.Open(m.TOTPSecret)
	if err != nil || !admin.VerifyTOTP(secret, in.Code, now) {
		apierr.Write(w, apierr.InvalidCode, "Código errado. Confirma a hora do telemóvel e tenta outra vez.", "code")
		return
	}
	if err := a.Team.Activate(r.Context(), m.ID, name, hash); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível aceitar o convite.")
		return
	}
	m.Name, m.TOTPEnabled = name, true
	a.startSession(w, r, m, "invite_accepted")
}
