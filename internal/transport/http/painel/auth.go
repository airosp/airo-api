package painel

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/platform/email"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

const (
	codeTTL = 10 * time.Minute
	lockFor = 15 * time.Minute
)

// Mailer entrega os emails do painel.
type Mailer interface {
	Send(ctx context.Context, m email.Message) error
}

type memberView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Email        string  `json:"email"`
	Role         string  `json:"role"`
	SpecialistID *string `json:"specialistId"`
	Active       bool    `json:"active"`
	LastSeenAt   *string `json:"lastSeenAt"`
	CreatedAt    string  `json:"createdAt"`
	Initials     string  `json:"initials"`
	// Invited: ainda não entrou nenhuma vez.
	Invited bool `json:"invited"`
}

func viewMember(m repo.MemberRow) memberView {
	return memberView{
		ID: m.ID, Name: m.Name, Email: m.Email, Role: m.Role, SpecialistID: m.SpecialistID,
		Active: m.Active, LastSeenAt: iso(m.LastSeenAt),
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339), Initials: initialsOf(m.Name),
		Invited: m.LastSeenAt == nil,
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

// codeHash prende o código ao desafio: o mesmo código noutro pedido não vale.
func (a *API) codeHash(challenge, code string) ([]byte, error) {
	return auth.HashCode(challenge+":"+code, a.Pepper)
}

// Login é o primeiro passo: o email. Se for de um membro activo, chega-lhe um
// código de seis dígitos. A resposta a um email que não é da equipa é igual —
// um desafio que nunca aceitará código nenhum —, para o formulário não servir
// para descobrir quem lá trabalha.
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	addr := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(addr, "@") {
		invalid(w, "Escreve o teu email.", "email")
		return
	}
	if a.limited(r, "ip:"+ipKey(r), 30) || a.limited(r, "email:"+string(admin.HashToken(addr)), 6) {
		apierr.Write(w, apierr.RateLimited, "Demasiados pedidos de código. Tenta daqui a uma hora.", "")
		return
	}
	now := a.now()
	token, hash, err := admin.NewToken()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível enviar o código.")
		return
	}
	reply := map[string]any{"challenge": token, "expiresAt": now.Add(codeTTL).Format(time.RFC3339)}

	m, err := a.Team.MemberByEmail(r.Context(), addr)
	if errors.Is(err, repo.ErrNotFound) || (err == nil && !m.Active) {
		ok(w, reply)
		return
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível enviar o código.")
		return
	}
	if m.LockedUntil != nil && m.LockedUntil.After(now) {
		apierr.Write(w, apierr.Locked, "Entrada trancada depois de 5 códigos errados. Tenta daqui a 15 minutos.", "")
		return
	}
	if a.Mail == nil {
		apierr.Write(w, apierr.Unavailable, "O envio de emails não está configurado.", "")
		return
	}
	code, err := auth.GenerateCode()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível enviar o código.")
		return
	}
	codeHash, err := a.codeHash(token, code)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível enviar o código.")
		return
	}
	if err := a.Team.NewChallenge(r.Context(), hash, codeHash, m.ID, now.Add(codeTTL)); err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível enviar o código.")
		return
	}
	if err := a.Mail.Send(r.Context(), codeEmail(m, code)); err != nil {
		if a.Log != nil {
			a.Log.Error("painel: enviar código", "error", err)
		}
		apierr.Write(w, apierr.Unavailable, "Não foi possível enviar o email. Tenta outra vez daqui a pouco.", "")
		return
	}
	a.auditAs(r, m, "code_sent", m.Email, "")
	ok(w, reply)
}

// Verify é o segundo passo: o código que chegou por email.
func (a *API) Verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	if a.limited(r, "verify:"+ipKey(r), 60) {
		apierr.Write(w, apierr.RateLimited, "Demasiadas tentativas. Tenta daqui a uma hora.", "")
		return
	}
	now := a.now()
	code := strings.Join(strings.Fields(in.Code), "")
	hash := admin.HashToken(in.Challenge)
	memberID, stored, attempts, err := a.Team.Challenge(r.Context(), hash, now)
	if errors.Is(err, repo.ErrNotFound) {
		// Inclui o email que não é da equipa: o código nunca existiu.
		apierr.Write(w, apierr.InvalidCode, "Código errado ou expirado. Confirma o email ou pede outro.", "code")
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
	want, err := a.codeHash(in.Challenge, code)
	if err != nil || !m.Active || len(code) != auth.CodeLength || !hmac.Equal(want, stored) {
		if attempts >= 5 {
			_ = a.Team.DropChallenge(r.Context(), hash)
			_ = a.Team.LoginFailed(r.Context(), m.ID, lockFor, now)
			a.auditAs(r, m, "login_blocked", m.Email, "5 códigos errados")
			apierr.Write(w, apierr.ChallengeExpired, "5 códigos errados. Pede um código novo.", "code")
			return
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

// ── Emails ───────────────────────────────────────────────────────────────────

func emailLayout(title, body string) string {
	return `<!doctype html><html lang="pt"><body style="margin:0;background:#f5f5f4;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1c1917">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border-radius:16px;padding:32px">
<tr><td style="font-size:13px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:#78716c">Airo · Equipa</td></tr>
<tr><td style="padding-top:12px;font-size:22px;font-weight:600">` + title + `</td></tr>
<tr><td style="padding-top:12px;font-size:15px;line-height:1.55;color:#44403c">` + body + `</td></tr>
</table>
<p style="font-size:12px;color:#a8a29e;margin-top:16px">Recebeste este email porque fazes parte da equipa da Airo.</p>
</td></tr></table></body></html>`
}

func codeEmail(m repo.MemberRow, code string) email.Message {
	first := strings.Fields(m.Name)
	hello := "Olá"
	if len(first) > 0 {
		hello = "Olá, " + html.EscapeString(first[0])
	}
	body := hello + `. Este é o teu código para entrar no painel:
<div style="margin:20px 0;font-size:34px;font-weight:700;letter-spacing:.3em;font-family:ui-monospace,Menlo,monospace">` + code + `</div>
Vale durante 10 minutos. Se não foste tu a pedi-lo, ignora este email — ninguém entra sem o código.`
	return email.Message{
		To:      m.Email,
		Subject: fmt.Sprintf("%s é o teu código do painel Airo", code),
		HTML:    emailLayout("O teu código de entrada", body),
	}
}

func welcomeEmail(m repo.MemberRow, by, role, panelURL string) email.Message {
	link := html.EscapeString(strings.TrimRight(panelURL, "/") + "/entrar")
	body := html.EscapeString(by) + ` deu-te acesso ao painel da Airo, com a função <b>` + html.EscapeString(role) + `</b>.
<div style="margin:24px 0"><a href="` + link + `" style="display:inline-block;background:#1c1917;color:#ffffff;text-decoration:none;padding:12px 20px;border-radius:10px;font-weight:600">Abrir o painel</a></div>
Entra com este email (` + html.EscapeString(m.Email) + `): enviamos-te um código de seis dígitos de cada vez. Não há palavra-passe.`
	return email.Message{
		To:      m.Email,
		Subject: "Tens acesso ao painel da Airo",
		HTML:    emailLayout("Bem-vindo à equipa", body),
	}
}
