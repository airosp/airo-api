package painel

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

var roleLabels = map[admin.Role]string{
	admin.RoleAdmin: "Administração", admin.RoleContent: "Conteúdo", admin.RoleNutrition: "Nutrição",
	admin.RoleSupport: "Suporte", admin.RoleSpecialist: "Especialista",
}

func (a *API) TeamList(w http.ResponseWriter, r *http.Request) {
	ms, err := a.Team.Members(r.Context())
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler a equipa.")
		return
	}
	out := make([]memberView, 0, len(ms))
	for _, m := range ms {
		out = append(out, viewMember(m))
	}
	roles := []map[string]string{}
	for _, ro := range admin.Roles {
		roles = append(roles, map[string]string{"key": string(ro), "label": roleLabels[ro]})
	}
	ok(w, map[string]any{"members": out, "matrix": map[string]any{"roles": roles, "areas": admin.Matrix}})
}

// invite cria (ou repõe) um membro e devolve o link. O painel não manda
// emails: quem convida copia o link e envia-o pelo canal que já usa com a
// pessoa.
func (a *API) invite(w http.ResponseWriter, r *http.Request, email, name, role string, specialistID *string) {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil {
		invalid(w, "Email inválido.", "email")
		return
	}
	email = strings.ToLower(addr.Address)
	if !admin.ValidRole(role) {
		invalid(w, "Função desconhecida.", "role")
		return
	}
	if role == string(admin.RoleSpecialist) && (specialistID == nil || *specialistID == "") {
		invalid(w, "Um especialista tem de estar ligado à sua ficha.", "specialistId")
		return
	}
	if role != string(admin.RoleSpecialist) {
		specialistID = nil
	}
	if strings.TrimSpace(name) == "" {
		name = strings.Split(email, "@")[0]
	}
	token, hash, err := admin.NewToken()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível criar o convite.")
		return
	}
	m, err := a.Team.Invite(r.Context(), email, strings.TrimSpace(name), role, specialistID, hash, a.now().Add(inviteTTL))
	if err != nil {
		writeRepoErr(w, r, err, "o membro")
		return
	}
	a.audit(r, "member_invited", email, "Função: "+roleLabels[admin.Role(role)])
	created(w, map[string]any{"inviteUrl": a.inviteURL(token), "member": viewMember(m)})
}

func (a *API) TeamInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email        string  `json:"email"`
		Name         string  `json:"name"`
		Role         string  `json:"role"`
		SpecialistID *string `json:"specialistId"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	if existing, err := a.Team.MemberByEmail(r.Context(), strings.ToLower(strings.TrimSpace(in.Email))); err == nil && existing.TOTPEnabled {
		apierr.Write(w, apierr.Conflict, "Esta pessoa já tem conta. Usa \"Repor acesso\" se perdeu a app autenticadora.", "email")
		return
	}
	a.invite(w, r, in.Email, in.Name, in.Role, in.SpecialistID)
}

func (a *API) TeamUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role   *string `json:"role"`
		Active *bool   `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		invalid(w, "Pedido inválido.", "")
		return
	}
	id := r.PathValue("id")
	me, _ := memberOf(r.Context())
	if in.Role != nil && !admin.ValidRole(*in.Role) {
		invalid(w, "Função desconhecida.", "role")
		return
	}
	target, err := a.Team.MemberByID(r.Context(), id)
	if err != nil {
		writeRepoErr(w, r, err, "o membro")
		return
	}
	if in.Role != nil && *in.Role == string(admin.RoleSpecialist) && target.SpecialistID == nil {
		invalid(w, "Para ser especialista, convida-o a partir da ficha do especialista.", "role")
		return
	}
	// Ninguém se tranca fora sozinho, e a equipa nunca fica sem administração.
	demote := (in.Role != nil && *in.Role != string(admin.RoleAdmin)) || (in.Active != nil && !*in.Active)
	if target.Role == string(admin.RoleAdmin) && demote {
		if id == me.ID {
			apierr.Write(w, apierr.Conflict, "Não podes tirar a tua própria administração.", "")
			return
		}
		n, err := a.Team.CountAdmins(r.Context())
		if err != nil {
			apierr.WriteInternal(w, r, err, "Não foi possível gravar.")
			return
		}
		if n <= 1 {
			apierr.Write(w, apierr.Conflict, "A equipa ficaria sem administração.", "")
			return
		}
	}
	m, err := a.Team.UpdateMember(r.Context(), id, in.Role, in.Active)
	if err != nil {
		writeRepoErr(w, r, err, "o membro")
		return
	}
	detail := []string{}
	if in.Role != nil {
		detail = append(detail, "Função: "+roleLabels[admin.Role(*in.Role)])
	}
	if in.Active != nil {
		if *in.Active {
			detail = append(detail, "Activado")
		} else {
			detail = append(detail, "Desactivado — sessões terminadas")
		}
	}
	a.audit(r, "member_updated", m.Email, strings.Join(detail, " · "))
	ok(w, viewMember(m))
}

// TeamReset repõe a palavra-passe e a app autenticadora de alguém que as
// perdeu. As sessões abertas caem no mesmo instante.
func (a *API) TeamReset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := a.Team.MemberByID(r.Context(), id)
	if err != nil {
		writeRepoErr(w, r, err, "o membro")
		return
	}
	token, hash, err := admin.NewToken()
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível repor o acesso.")
		return
	}
	if err := a.Team.ResetInvite(r.Context(), id, hash, a.now().Add(inviteTTL)); err != nil {
		writeRepoErr(w, r, err, "o membro")
		return
	}
	a.audit(r, "member_reset", m.Email, "Palavra-passe e app autenticadora repostas")
	ok(w, map[string]any{"inviteUrl": a.inviteURL(token)})
}

var actionLabels = map[string]string{
	"login": "entrou no painel", "logout": "saiu do painel", "login_failed": "falhou a palavra-passe",
	"login_blocked": "ficou trancado", "invite_accepted": "aceitou o convite",
	"member_invited": "convidou", "member_updated": "alterou o acesso de", "member_reset": "repôs o acesso de",
	"user_record_opened": "abriu a ficha de", "specialist_saved": "gravou o especialista",
	"specialist_photo": "mudou a fotografia de", "class_saved": "gravou a aula",
	"class_upload": "enviou o vídeo da aula", "class_published": "publicou a aula",
	"class_unpublished": "despublicou a aula", "class_deleted": "apagou a aula",
	"playlist_saved": "gravou a playlist", "playlist_deleted": "apagou a playlist",
	"exercise_saved": "gravou o exercício", "food_saved": "gravou o alimento",
	"recipe_saved": "gravou o prato", "recipe_validated": "aprovou o prato",
	"template_saved": "gravou o modelo de treino", "template_deleted": "apagou o modelo de treino",
	"diet_saved": "gravou o modelo de dieta", "diet_status": "mudou o estado do modelo de dieta",
}

func (a *API) AuditLog(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	rows, err := a.Team.AuditLog(r.Context(), limit, before)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler a auditoria.")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, x := range rows {
		var actor any
		if x.ActorName != nil {
			actor = map[string]any{"id": x.MemberID, "name": *x.ActorName}
		}
		label := actionLabels[x.Action]
		if label == "" {
			label = x.Action
		}
		items = append(items, map[string]any{
			"id": x.ID, "at": x.At.UTC().Format(time.RFC3339), "actor": actor,
			"action": x.Action, "label": label, "target": x.Target, "detail": x.Detail,
		})
	}
	ok(w, map[string]any{"items": items})
}
