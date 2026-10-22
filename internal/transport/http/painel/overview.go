package painel

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (a *API) Overview(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days != 30 && days != 90 {
		days = 7
	}
	now := a.now()
	o, err := a.Users.Overview(r.Context(), days, now)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível montar a visão geral.")
		return
	}
	m, _ := memberOf(r.Context())

	var adh, adhPrev any
	if o.AvgAdherence != nil {
		adh = round2(*o.AvgAdherence)
	}
	if o.AvgAdherencePrev != nil {
		adhPrev = round2(*o.AvgAdherencePrev)
	}
	byDay := []map[string]any{}
	for _, d := range o.ByDay {
		byDay = append(byDay, map[string]any{"day": day(d.Day), "people": d.People})
	}
	labels := []struct{ k, l string }{
		{"signup", "Conta criada"}, {"profile", "Perfil completo"}, {"goal", "Objetivo definido"},
		{"firstWorkout", "1.º treino"}, {"week2", "Activo na 2.ª semana"},
	}
	funnel := []map[string]any{}
	for i, s := range labels {
		funnel = append(funnel, map[string]any{"key": s.k, "label": s.l, "people": o.Funnel[i]})
	}

	attention := []map[string]any{}
	add := func(sev, title, detail, screen string, id any) {
		link := map[string]any{"screen": screen}
		if id != nil {
			link["id"] = id
		}
		attention = append(attention, map[string]any{"severity": sev, "title": title, "detail": detail, "link": link})
	}
	if admin.Can(m.role(), admin.AreaUsers, admin.View) {
		for i, id := range o.HighRiskIDs {
			if i >= 4 {
				add("critical", fmt.Sprintf("Mais %d pessoas com risco alto", o.HighRisk-4), "Esta semana", "users", nil)
				break
			}
			name := o.HighRiskNames[i]
			if name == "" {
				name = "Pessoa sem nome"
			}
			add("critical", name+" com risco alto", "O motor detectou um risco alto na última avaliação", "user", id)
		}
	}
	if admin.Can(m.role(), admin.AreaClasses, admin.View) {
		if o.ClassFailed > 0 {
			add("critical", plural(o.ClassFailed, "aula", "aulas")+" com o vídeo recusado pelo Mux", "Volta a enviar o ficheiro", "classes", nil)
		}
		if o.ClassWaiting > 0 {
			add("warning", plural(o.ClassWaiting, "aula", "aulas")+" à espera do vídeo há mais de uma hora", "O envio pode ter ficado a meio", "classes", nil)
		}
		if o.ClassReadyUnpublished > 0 {
			add("info", plural(o.ClassReadyUnpublished, "aula pronta", "aulas prontas")+" por publicar", "O vídeo já está processado", "classes", nil)
		}
	}
	if admin.Can(m.role(), admin.AreaNutrition, admin.View) {
		if o.RecipesToValidate > 0 {
			add("warning", plural(o.RecipesToValidate, "prato", "pratos")+" por validar", "A nutricionista ainda não os aprovou", "recipes", nil)
		}
		if o.DietsInReview > 0 {
			add("info", plural(o.DietsInReview, "modelo de dieta", "modelos de dieta")+" em revisão", "", "diets", nil)
		}
	}
	budget := float64(a.AIConfig.Budget.MonthlyMicroUSD) / 1e6
	spent := float64(o.AISpentMicro) / 1e6
	if admin.Can(m.role(), admin.AreaAI, admin.View) && budget > 0 && spent >= budget*0.8 {
		add("warning", fmt.Sprintf("A IA já gastou %.0f%% do orçamento do mês", spent/budget*100), "Chegando aos 100% pára sozinha", "ai", nil)
	}

	ok(w, map[string]any{
		"period": map[string]any{"days": days, "from": day(now.AddDate(0, 0, -days+1)), "to": day(now)},
		"kpis": map[string]any{
			"activePeople": map[string]any{"value": o.Active, "previous": o.ActivePrev},
			"workoutsDone": map[string]any{"value": o.Workouts, "previous": o.WorkoutsPrev},
			"adherence":    map[string]any{"value": adh, "previous": adhPrev},
			"foodLogs":     map[string]any{"value": o.Logs, "previous": o.LogsPrev},
			"totalPeople":  o.Total,
		},
		"activeByDay": byDay,
		"funnel":      funnel,
		"attention":   attention,
		"ai": map[string]any{
			"enabled": a.AIConfig.Features.Enabled && a.Services.AI, "photoEnabled": a.AIConfig.Features.MealPhoto,
			"textEnabled": a.AIConfig.Features.MealText, "budgetUsd": round2(budget), "spentUsd": round2(spent),
			"runs": o.AIRuns, "applied": o.AIApplied, "corrected": o.AICorrected,
			"dismissed": o.AIDismissed, "failed": o.AIFailed,
		},
		"services": a.services(r.Context()),
	})
}

func plural(n int, um, varios string) string {
	if n == 1 {
		return "1 " + um
	}
	return strconv.Itoa(n) + " " + varios
}

func (a *API) services(ctx context.Context) []map[string]any {
	ping := func(p Pinger) (bool, string) {
		if p == nil {
			return false, "Não configurado"
		}
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		t := time.Now()
		if err := p.Ping(c); err != nil {
			return false, "Sem resposta"
		}
		return true, fmt.Sprintf("%d ms", time.Since(t).Milliseconds())
	}
	conf := func(b bool) (bool, string) {
		if b {
			return true, "Configurado"
		}
		return false, "Não configurado"
	}
	out := []map[string]any{}
	for _, s := range []struct {
		key, label string
		fn         func() (bool, string)
	}{
		{"database", "Base de dados", func() (bool, string) { return ping(a.Services.DB) }},
		{"cache", "Cache (Redis)", func() (bool, string) { return ping(a.Services.Cache) }},
		{"mux", "Vídeo (Mux)", func() (bool, string) { return conf(a.Services.Mux) }},
		{"whatsapp", "Códigos (WhatsApp)", func() (bool, string) { return conf(a.Services.WhatsApp) }},
		{"cloudinary", "Fotografias (Cloudinary)", func() (bool, string) { return conf(a.Services.Cloudinary) }},
		{"openai", "Inteligência (OpenAI)", func() (bool, string) { return conf(a.Services.AI) }},
	} {
		okv, detail := s.fn()
		out = append(out, map[string]any{"key": s.key, "label": s.label, "ok": okv, "detail": strings.TrimSpace(detail)})
	}
	return out
}
