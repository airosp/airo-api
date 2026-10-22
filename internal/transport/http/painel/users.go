package painel

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/airosp/airo-api/internal/admin"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

var goalLabels = map[string]string{
	"lose_weight": "Perder peso", "gain_weight": "Ganhar massa", "maintain_weight": "Manter o peso",
}

var riskLabels = map[string]string{
	"low_adherence": "Adesão baixa", "rapid_change": "Mudança rápida de peso", "plateau": "Peso parado",
	"volume_spike": "Salto de volume", "stalled_start": "Início parado", "no_response": "Sem resposta ao plano",
	"under_recovery": "Pouca recuperação",
}

var roleOrder = map[string]string{"trainer": "Treinador", "nutritionist": "Nutricionista", "physio": "Fisioterapeuta", "coach": "Coach"}

// maskPhone mostra o indicativo e os dois últimos dígitos. O número inteiro
// não sai do servidor: quem precisa de ligar a alguém fá-lo pela app.
func maskPhone(e164 string) string {
	d := strings.TrimPrefix(e164, "+")
	if len(d) < 6 {
		return "•••"
	}
	cc := d[:3]
	if strings.HasPrefix(d, "1") || strings.HasPrefix(d, "7") {
		cc = d[:1]
	} else if len(d) <= 11 {
		cc = d[:2]
	}
	return "+" + cc + " " + d[len(cc):len(cc)+2] + " ••• •• " + d[len(d)-2:]
}

func weekOf(start time.Time, now time.Time) int {
	w := int(now.Sub(start).Hours()/24)/7 + 1
	if w < 1 {
		return 1
	}
	return w
}

func totalWeeks(start time.Time, target *time.Time, cycle *int) *int {
	if target != nil {
		n := int(math.Ceil(target.Sub(start).Hours() / 24 / 7))
		return &n
	}
	return nil
}

type userItem struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Initials      string           `json:"initials"`
	PhoneMasked   string           `json:"phoneMasked"`
	PhotoURL      *string          `json:"photoUrl"`
	Goal          map[string]any   `json:"goal"`
	Journey       map[string]any   `json:"journey"`
	Adherence7    *float64         `json:"adherence7"`
	LastWorkoutAt *string          `json:"lastWorkoutAt"`
	LastSeenAt    *string          `json:"lastSeenAt"`
	CreatedAt     string           `json:"createdAt"`
	Alert         map[string]any   `json:"alert"`
	Team          []map[string]any `json:"team"`

	risk, low, stalled, paused, noSpec bool
}

func (a *API) buildUser(u repo.AdminUserRow, now time.Time) userItem {
	name := u.Name
	if name == "" {
		name = "Sem nome"
	}
	it := userItem{
		ID: u.ID, Name: name, Initials: initialsOf(name), PhoneMasked: maskPhone(u.Phone),
		PhotoURL: u.PhotoURL, LastWorkoutAt: iso(u.LastWorkoutAt), LastSeenAt: iso(u.LastSeenAt),
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339), Team: []map[string]any{},
	}
	if u.Direction != nil {
		it.Goal = map[string]any{"direction": *u.Direction, "label": goalLabels[*u.Direction]}
	}
	if u.JourneyStart != nil && u.JourneyStatus != nil {
		it.Journey = map[string]any{
			"week": weekOf(*u.JourneyStart, now), "totalWeeks": totalWeeks(*u.JourneyStart, u.JourneyTarget, u.CycleWeeks),
			"status": *u.JourneyStatus,
		}
		it.paused = *u.JourneyStatus == "paused"
		// A adesão só se mede a quem já tem uma semana de jornada: julgar quem
		// começou ontem dava 0% a quem ainda não teve como treinar.
		if now.Sub(*u.JourneyStart) >= 7*24*time.Hour && !it.paused {
			v := math.Min(1, float64(u.Done7)/float64(u.DaysPerWeek))
			v = math.Round(v*100) / 100
			it.Adherence7 = &v
			it.low = v < 0.5
		}
	}
	if u.RiskLevel != nil && u.RiskType != nil && (*u.RiskLevel == "high" || *u.RiskLevel == "medium") {
		label := riskLabels[*u.RiskType]
		if label == "" {
			label = *u.RiskType
		}
		it.Alert = map[string]any{"level": *u.RiskLevel, "type": *u.RiskType, "label": label}
		it.risk = true
	}
	// Início parado: perfil feito há mais de três dias e nenhum treino.
	it.stalled = u.ProfileDone && u.TotalWorkouts == 0 && now.Sub(u.CreatedAt) > 72*time.Hour
	if it.stalled && it.Alert == nil {
		it.Alert = map[string]any{"level": "low", "type": "stalled_start", "label": riskLabels["stalled_start"]}
	}
	for i := range u.TeamIDs {
		it.Team = append(it.Team, map[string]any{
			"id": u.TeamIDs[i], "name": u.TeamNames[i], "initials": u.TeamInitials[i], "role": u.TeamRoles[i],
		})
	}
	it.noSpec = len(u.TeamIDs) == 0
	return it
}

func (a *API) UsersList(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	now := a.now()
	rows, err := a.Users.Users(r.Context(), m.specialistScope(), now)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as pessoas.")
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, q)
	segment := r.URL.Query().Get("segment")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if size < 1 || size > 100 {
		size = 25
	}

	counts := map[string]int{}
	all := []userItem{}
	for _, row := range rows {
		it := a.buildUser(row, now)
		// A procura pelo telefone faz-se pelos dígitos que se vêem e pelos
		// últimos do número — nunca devolvendo o número.
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) &&
			!(len(digits) >= 4 && strings.HasSuffix(row.Phone, digits)) {
			continue
		}
		counts["all"]++
		flags := map[string]bool{"risk": it.risk, "lowAdherence": it.low, "stalled": it.stalled, "paused": it.paused, "noSpecialist": it.noSpec}
		for k, v := range flags {
			if v {
				counts[k]++
			}
		}
		if segment != "" && segment != "all" && !flags[segment] {
			continue
		}
		all = append(all, it)
	}
	// Quem precisa de atenção primeiro.
	rank := func(it userItem) int {
		if it.Alert == nil {
			return 3
		}
		switch it.Alert["level"] {
		case "high":
			return 0
		case "medium":
			return 1
		}
		return 2
	}
	sort.SliceStable(all, func(i, j int) bool { return rank(all[i]) < rank(all[j]) })

	total := len(all)
	from := (page - 1) * size
	if from > total {
		from = total
	}
	to := from + size
	if to > total {
		to = total
	}
	segs := map[string]int{}
	for _, k := range []string{"all", "risk", "lowAdherence", "stalled", "paused", "noSpecialist"} {
		segs[k] = counts[k]
	}
	ok(w, map[string]any{"items": all[from:to], "total": total, "page": page, "pageSize": size, "segments": segs})
}

// UserRecord abre a ficha. Exige um motivo, e o motivo fica na auditoria: a
// ficha tem dados de saúde, e abri-la é uma decisão que alguém tem de poder
// explicar depois.
func (a *API) UserRecord(w http.ResponseWriter, r *http.Request) {
	m, _ := memberOf(r.Context())
	reason := strings.TrimSpace(r.Header.Get("X-Admin-Reason"))
	if utf8.RuneCountInString(reason) < 5 {
		apierr.Write(w, apierr.ReasonRequired, "Indica o motivo para abrir esta ficha.", "reason")
		return
	}
	if utf8.RuneCountInString(reason) > 200 {
		reason = string([]rune(reason)[:200])
	}
	id := r.PathValue("id")
	if scope := m.specialistScope(); scope != "" {
		can, err := a.Users.CanSee(r.Context(), scope, id)
		if err != nil {
			apierr.WriteInternal(w, r, err, "Não foi possível abrir a ficha.")
			return
		}
		if !can {
			apierr.Write(w, apierr.Forbidden, "Esta pessoa não te escolheu como especialista.", "")
			return
		}
	}
	now := a.now()
	rec, err := a.Users.Record(r.Context(), id, now)
	if errors.Is(err, repo.ErrNotFound) {
		apierr.Write(w, apierr.NotFound, "Pessoa não encontrada.", "")
		return
	}
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível abrir a ficha.")
		return
	}
	name := rec.Name
	if name == "" {
		name = "Sem nome"
	}
	a.audit(r, "user_record_opened", name, "Motivo: "+reason)

	out := map[string]any{
		"user": map[string]any{
			"id": rec.ID, "name": name, "initials": initialsOf(name), "phoneMasked": maskPhone(rec.Phone),
			"photoUrl": rec.PhotoURL, "createdAt": rec.CreatedAt.UTC().Format(time.RFC3339), "lastSeenAt": iso(rec.LastSeenAt),
		},
		"profile": rec.Profile,
	}

	// O retrato do motor: adesão e riscos são os mesmos que a pessoa vê.
	var snap *service.Snapshot
	if a.Progress != nil && a.Profiles != nil && rec.JourneyID != nil {
		if perfil, err := a.Profiles.Profile(r.Context(), id); err == nil || errors.Is(err, service.ErrWeightMissing) {
			dias, _ := a.Profiles.TrainingDaysOf(r.Context(), id)
			s, err := a.Progress.Snapshot(r.Context(), service.SnapshotInput{
				UserID: id, DaysPerWeek: perfil.DaysPerWeek, SessionMinutes: perfil.SessionMinutes,
				TrainingDays: dias, WeightKg: perfil.CurrentWeightKg, Now: now,
			})
			if err == nil {
				snap = &s
			} else if a.Log != nil && !errors.Is(err, service.ErrSemJornada) {
				a.Log.Warn("ficha: retrato de progresso", "error", err)
			}
		}
	}

	weights := []map[string]any{}
	var current *float64
	for _, wv := range rec.Weights {
		weights = append(weights, map[string]any{"day": day(wv.Day), "kg": wv.Value})
		v := wv.Value
		current = &v
	}
	out["weights"] = weights

	if rec.Direction != nil && rec.Start != nil {
		g := map[string]any{
			"direction": *rec.Direction, "label": goalLabels[*rec.Direction],
			"startDate": day(*rec.Start), "targetDate": nil, "week": weekOf(*rec.Start, now),
			"totalWeeks": totalWeeks(*rec.Start, rec.Target, rec.CycleWeeks), "status": rec.Status,
			"startWeightKg": nil, "currentWeightKg": current, "targetWeightKg": nil,
		}
		if rec.Target != nil {
			g["targetDate"] = day(*rec.Target)
		}
		if snap != nil {
			if snap.Baseline != nil {
				g["startWeightKg"] = *snap.Baseline
			}
			if snap.Target != nil {
				g["targetWeightKg"] = *snap.Target
			}
		}
		out["goal"] = g
	} else {
		out["goal"] = nil
	}

	// Cinco semanas de calendário, de segunda a domingo, a acabar hoje.
	training := map[string]string{}
	for _, d := range rec.Training {
		training[day(d.Day)] = d.Status
	}
	workDays := map[int]bool{}
	if wd, ok := rec.Profile["workoutDays"].([]any); ok {
		for _, x := range wd {
			if f, ok := x.(float64); ok {
				workDays[int(f)] = true
			}
		}
	}
	food := map[string]repo.DayFood{}
	for _, d := range rec.Food {
		food[day(d.Day)] = d
	}
	var target *int
	if rec.Strategy != nil {
		target = &rec.Strategy.Kcal
	}
	tDays, fDays := []map[string]any{}, []map[string]any{}
	for i := 34; i >= 0; i-- {
		d := now.AddDate(0, 0, -i)
		k := day(d)
		st := training[k]
		if st == "" {
			// O perfil guarda o dia da semana com 0 = segunda.
			wd := (int(d.Weekday()) + 6) % 7
			if workDays[wd] {
				st = "planned"
			} else {
				st = "rest"
			}
		}
		tDays = append(tDays, map[string]any{"day": k, "status": st})
		f := food[k]
		fDays = append(fDays, map[string]any{"day": k, "logs": f.Logs, "kcal": f.Kcal, "targetKcal": target})
	}
	out["trainingDays"] = tDays
	out["foodDays"] = fDays

	sessions := []map[string]any{}
	for _, s := range rec.Sessions {
		sessions = append(sessions, map[string]any{
			"id": s.ID, "title": s.Title, "focus": s.Focus, "status": s.Status, "day": day(s.Day),
			"durationSeconds": s.Duration, "plannedSeconds": s.Planned, "setsDone": s.Done,
			"setsPlanned": s.SetsPl, "kcal": s.Kcal,
		})
	}
	out["sessions"] = sessions

	// A adesão alimentar dos últimos 7 dias: dias com registo sobre sete.
	logged := 0
	for i := 0; i < 7; i++ {
		if food[day(now.AddDate(0, 0, -i))].Logs > 0 {
			logged++
		}
	}
	nutr := math.Round(float64(logged)/7*100) / 100
	adh := map[string]any{"score": nil, "training": nil, "nutrition": nutr}
	risks := []map[string]any{}
	if snap != nil {
		if snap.Adherence.Evaluable {
			adh["score"] = math.Round(snap.Adherence.Score*100) / 100
			adh["training"] = math.Round(snap.Adherence.Frequency*100) / 100
		}
		for _, rk := range snap.Risks {
			label := riskLabels[string(rk.Type)]
			if label == "" {
				label = string(rk.Type)
			}
			risks = append(risks, map[string]any{
				"type": rk.Type, "level": rk.Level, "label": label,
				"evidence": rk.Evidence, "recommendation": rk.Recommendation,
			})
		}
	}
	out["adherence"] = adh
	out["risks"] = risks

	adapts := []map[string]any{}
	for _, ad := range rec.Adapt {
		reason, _ := ad.Payload["reason"].(string)
		adapts = append(adapts, map[string]any{
			"id": ad.ID, "kind": ad.Kind, "label": adaptationLabel(ad.Kind), "reason": reason,
			"createdAt": ad.CreatedAt.UTC().Format(time.RFC3339), "status": ad.Status,
		})
	}
	out["adaptations"] = adapts

	if rec.Strategy != nil {
		out["nutrition"] = map[string]any{
			"calorieTarget": rec.Strategy.Kcal, "proteinG": rec.Strategy.Protein, "carbsG": rec.Strategy.Carbs,
			"fatG": rec.Strategy.Fat, "tdeeEstimated": rec.Strategy.TDEE,
		}
	} else {
		out["nutrition"] = nil
	}
	team := []map[string]any{}
	for _, t := range rec.Team {
		team = append(team, map[string]any{
			"id": t.ID, "name": t.Name, "initials": t.Initials, "role": t.Role,
			"roleLabel": roleOrder[t.Role], "invitedAt": t.InvitedAt.UTC().Format(time.RFC3339),
		})
	}
	out["team"] = team
	ok(w, out)
}

func adaptationLabel(kind string) string {
	l := map[string]string{
		"maintain": "Manter o plano", "reduce_load": "Reduzir a carga", "increase_load": "Aumentar a carga",
		"simplify": "Simplificar", "extend_timeframe": "Alargar o prazo", "review_goal": "Rever o objetivo",
		"reduce_frequency": "Menos treinos por semana", "increase_frequency": "Mais treinos por semana",
		"reduce_duration": "Treinos mais curtos", "increase_duration": "Treinos mais longos",
		"reduce_intensity": "Menos intensidade", "increase_intensity": "Mais intensidade",
		"deload": "Semana de descarga", "extend_timeline": "Alargar o prazo", "hold": "Manter",
		"calorie_up": "Subir as calorias", "calorie_down": "Descer as calorias", "recalibrate": "Recalibrar",
	}[kind]
	if l == "" {
		return kind
	}
	return l
}

var _ = admin.AreaUsers
var _ = fmt.Sprintf
