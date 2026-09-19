package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/domain"
	"github.com/airosp/airo-api/internal/engine/training"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/dto"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// TrainingProfileReader dá ao handler o que o motor precisa e o pedido não traz.
//
// O rótulo do dia, o equipamento e os exercícios fixados vêm do plano e do
// perfil. Recebê-los no corpo era deixar o cliente escolher o treino — e o
// treino é uma decisão.
type TrainingProfileReader interface {
	TrainingProfile(ctx contextLike, userID string, day time.Time) (service.TodayInput, error)
}

// SessionHistory lê o histórico de treinos.
//
// Interface e não o repositório: o handler não tem de saber que existe
// Postgres, e um teste do transporte não tem de levantar uma base de dados
// para provar que o intervalo é validado.
type SessionHistory interface {
	History(ctx context.Context, userID string, from, to time.Time) ([]repo.HistoryRow, error)
	// PerformedIn traz o que se fez em cada exercício — a carga incluída.
	PerformedIn(ctx context.Context, userID string, from, to time.Time) ([]repo.PerformedRow, error)
	// LastLoads traz a última carga de cada exercício, para a propor.
	LastLoads(ctx context.Context, userID string) ([]repo.UltimaCarga, error)
}

type Training struct {
	Service  *service.TrainingService
	Profiles TrainingProfileReader
	Sessions SessionHistory
	// Classes serve as aulas gravadas. Numa aula é ela que diz quanto tempo o
	// treino pedia — não o motor, e muito menos o cliente.
	Classes ClassStore
	// Training traduz o rótulo do dia no foco, para escolher a aula certa.
	Training training.Config
	// Video monta o endereço da aula quando o dia é uma aula.
	Video VideoSource
}

// seedDoDia — os últimos quatro dígitos da data, como no resto do sistema.
//
// A mesma data dá a mesma aula em dois telemóveis, e amanhã dá outra. Escolher
// ao acaso fazia o treino de hoje mudar a cada vez que alguém abrisse o ecrã.
func seedDoDia(day time.Time) int {
	iso := day.Format("20060102")
	n := 0
	for _, r := range iso[len(iso)-4:] {
		n = n*10 + int(r-'0')
	}
	return n
}

func (h Training) Today(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	day, err := localDay(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}

	in, err := h.Profiles.TrainingProfile(r.Context(), userID, day)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	case err != nil:
		// Qualquer outro erro aqui é nosso. Dizer "perfil incompleto" a uma
		// coluna em falta mandou-me à procura no sítio errado durante uma
		// hora: o cliente lia uma instrução e o registo não dizia nada.
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return
	}
	in.LocalDay = day

	/*
	 * O dia é uma aula, ou é o plano — e a resposta diz qual.
	 *
	 * Não são as duas: dois treinos para o mesmo dia é exactamente a confusão
	 * que isto existe para resolver. Quando há aula que sirva o foco, o nível e
	 * o equipamento, o dia é a aula e o motor não entra.
	 *
	 * Sem aula que sirva, o dia é o plano — e é essa salvaguarda que impede a
	 * Airo de marcar um dia de aula que não tem como encher.
	 */
	if h.Classes != nil {
		focus := string(h.Training.FocusOf(in.PlanLabel))
		aula, err := h.Classes.ForDay(r.Context(), focus, in.Experience, in.Equipment, seedDoDia(day))
		if err == nil {
			apierr.WriteJSON(w, http.StatusOK, map[string]any{
				"kind": "class", "class": paraAula(aula, h.Video),
			})
			return
		}
	}

	pkg, _, _, err := h.Service.Today(in)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível montar o treino de hoje.")
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"kind": "session", "session": pkg})
}

// History devolve as sessões de um intervalo de dias.
//
// É o que faz o histórico sobreviver a mudar de telemóvel. O `id` que volta é a
// chave de idempotência com que a sessão foi gravada — a mesma que o aparelho
// deu —, e é assim que ele reconhece o que já é seu em vez de duplicar.
func (h Training) History(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Sessions == nil {
		apierr.Write(w, apierr.Internal, "O histórico está indisponível.", "")
		return
	}

	from, err := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data inicial inválida.", "from")
		return
	}
	to, err := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Data final inválida.", "to")
		return
	}
	if to.Before(from) {
		apierr.Write(w, apierr.ValidationFailed, "O fim é antes do início.", "to")
		return
	}
	// Como no diário: sem limite, o pedido fica cada vez mais lento à medida
	// que a pessoa treina.
	if to.Sub(from) > 366*24*time.Hour {
		apierr.Write(w, apierr.ValidationFailed, "Pede no máximo um ano de cada vez.", "to")
		return
	}

	rows, err := h.Sessions.History(r.Context(), userID, from, to)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler o histórico.")
		return
	}

	/*
	 * O detalhe, numa consulta só.
	 *
	 * Se falhar, o histórico sai como sempre saiu: sem detalhe é menos do que
	 * se queria, mas **sem histórico** é um ecrã vazio a quem treinou.
	 */
	detalhe := map[string][]dto.PerformedExerciseView{}
	if feito, err := h.Sessions.PerformedIn(r.Context(), userID, from, to); err == nil {
		for _, p := range feito {
			v := dto.PerformedExerciseView{
				ExerciseID: p.ExerciseSlug, Name: p.ExerciseName,
				Sets: p.Sets, Target: porExtenso(p.Target),
			}
			for _, a := range p.Actuals {
				v.Done = append(v.Done, porExtenso(a))
			}
			// Sem série nenhuma com detalhe, o exercício não acrescenta nada ao
			// que o resumo da sessão já diz.
			if len(v.Done) > 0 {
				detalhe[p.SessionID] = append(detalhe[p.SessionID], v)
			}
		}
	}

	out := dto.SessionHistoryResponse{Sessions: make([]dto.SessionHistoryItem, 0, len(rows))}
	for _, s := range rows {
		item := dto.SessionHistoryItem{
			ID: s.ID, Title: s.Title, Focus: s.Focus, Status: s.Status,
			OccurredAt:     s.OccurredAt.UTC().Format(time.RFC3339),
			LocalDay:       s.LocalDay.Format("2006-01-02"),
			PlannedSeconds: s.PlannedSeconds, DurationSeconds: s.DurationSeconds,
			SetsPlanned: s.SetsPlanned, SetsDone: s.SetsDone,
			Kcal: s.Kcal, Exercises: s.ExerciseCount,
		}
		// A chave do telemóvel ganha ao identificador do servidor: é ela que o
		// aparelho conhece, e é por ela que reconhece o que já gravou.
		if s.IdempotencyKey != nil && *s.IdempotencyKey != "" {
			item.ID = *s.IdempotencyKey
		}
		if s.WarmupSeconds != nil || s.MainSeconds != nil || s.CooldownSeconds != nil {
			item.Blocks = &dto.SessionBlocks{
				WarmupSeconds:   valorOuZero(s.WarmupSeconds),
				MainSeconds:     valorOuZero(s.MainSeconds),
				CooldownSeconds: valorOuZero(s.CooldownSeconds),
			}
		}
		// O detalhe casa pelo identificador do servidor, e não pela chave do
		// telemóvel que acabou de substituir o `item.ID`.
		item.Performed = detalhe[s.ID]
		out.Sessions = append(out.Sessions, item)
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func valorOuZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func (h Training) Record(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	var req dto.RecordSessionRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}

	occurred, err := time.Parse(time.RFC3339, req.OccurredAt)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Instante inválido.", "occurredAt")
		return
	}
	day, err := time.Parse("2006-01-02", req.LocalDay)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}
	if req.PlannedSeconds <= 0 {
		apierr.Write(w, apierr.ValidationFailed, "A sessão tem de dizer quanto tempo pedia.", "plannedSeconds")
		return
	}
	if req.DurationSeconds < 0 {
		apierr.Write(w, apierr.ValidationFailed, "A duração não pode ser negativa.", "durationSeconds")
		return
	}

	// A sessão é remontada aqui, a partir do plano — não vem no corpo. É o que
	// impede o cliente de gravar um treino que a Airo nunca propôs.
	profile, err := h.Profiles.TrainingProfile(r.Context(), userID, day)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto.", "")
		return
	}
	profile.LocalDay = day
	_, session, steps, err := h.Service.Today(profile)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível remontar a sessão.")
		return
	}

	in := service.RecordSessionInput{
		UserID:          userID,
		IdempotencyKey:  r.Header.Get("Idempotency-Key"),
		Title:           session.Title,
		Focus:           string(session.Focus),
		OccurredAt:      occurred,
		LocalDay:        day,
		PlannedSeconds:  req.PlannedSeconds,
		DurationSeconds: req.DurationSeconds,
		SetsPlanned:     countMainSets(steps),
		SetsDone:        req.SetsDone,
		Kcal:            session.EstimatedKcal,
		Prescriptions:   service.PrescriptionsFrom(session),
	}
	if req.Blocks != nil {
		in.WarmupSeconds, in.MainSeconds, in.CooldownSeconds =
			req.Blocks.WarmupSeconds, req.Blocks.MainSeconds, req.Blocks.CooldownSeconds
	}

	// O que se fez entra por cima do que se pediu. O alvo continua a ser o do
	// motor — adaptar amanhã não pode reescrever o que foi feito ontem.
	aplicarFeito(in.Prescriptions, req.Performed)

	/*
	 * Uma aula gravada é outra coisa, e grava-se de outra maneira.
	 *
	 * Numa aula **o vídeo lidera**: quem decidiu os exercícios, as séries e os
	 * descansos foi quem a filmou. Por isso o título, o foco, as calorias e —
	 * sobretudo — o **tempo planeado** vêm da aula, e não do motor nem do
	 * cliente. Deixar o cliente mandar o tempo planeado era deixá-lo dizer que
	 * uma aula de 38 minutos pedia cinco, e com isso decidir sozinho se contou.
	 */
	if req.ClassID != nil && *req.ClassID != "" && h.Classes != nil {
		aula, err := h.Classes.Get(r.Context(), *req.ClassID)
		if err != nil {
			apierr.Write(w, apierr.NotFound, "Essa aula não existe.", "classId")
			return
		}
		in.ClassID = req.ClassID
		in.Title = aula.Title
		in.Focus = aula.Focus
		in.PlannedSeconds = aula.DurationSeconds
		in.Kcal = aula.Kcal
		// Uma aula não tem prescrições: não foi montada, foi filmada. Sem isto,
		// o histórico dizia que uma aula tinha os exercícios do plano do dia.
		in.Prescriptions = nil
		in.SetsPlanned = 0
	}

	out, err := h.Service.Record(r.Context(), in)
	switch {
	case errors.Is(err, repo.ErrDuplicateSession):
		// Idempotência: 200 com o registo existente, não 409. Reenviar não é um
		// conflito — é a rede a voltar.
		apierr.WriteJSON(w, http.StatusOK, dto.RecordSessionResponse{
			ID: out.ID, Status: out.Status, CountsForStreak: out.CountsForStreak, Streak: out.Streak,
		})
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o treino.")
		return
	}

	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	apierr.WriteJSON(w, status, dto.RecordSessionResponse{
		ID: out.ID, Status: out.Status, CountsForStreak: out.CountsForStreak, Streak: out.Streak,
	})
}

// localDay lê o dia do utilizador do pedido. Sem ele, o servidor usaria o seu
// próprio dia — e o dia do servidor não é o de ninguém.
func localDay(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("localDay")
	if raw == "" {
		return time.Now().UTC().Truncate(24 * time.Hour), nil
	}
	return time.Parse("2006-01-02", raw)
}

// ── Sessão ao vivo ───────────────────────────────────────────────────────────

// Open abre a sessão do dia, para os eventos terem onde aterrar.
//
// É preciso porque `GET /v1/training/today` não grava nada: o pacote é montado
// e devolvido. Criar uma linha sempre que alguém espreita o treino de hoje
// encheria o histórico de sessões que ninguém fez.
func (h Training) Open(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	day, err := localDay(r)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}

	in, err := h.Profiles.TrainingProfile(r.Context(), userID, day)
	switch {
	case errors.Is(err, service.ErrProfileMissing):
		apierr.Write(w, apierr.ValidationFailed, "Perfil incompleto. Cria o teu plano primeiro.", "")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível ler o teu perfil.")
		return
	}
	in.LocalDay = day

	id, err := h.Service.Open(r.Context(), userID, in)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível abrir o treino.")
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]string{"sessionId": id})
}

// Events recebe o que aconteceu durante a sessão.
//
// O cliente acumula **factos** e envia-os em lote, inclusive depois de voltar a
// ter rede. Não conclui nada: quem decide se a sessão conta, se marca o dia e
// se soma à sequência é o servidor.
//
// ⚠️ `session_ended` não traz `status`. Traz `durationSeconds`. Deixar o cliente
// mandar `"completed"` devolvia-lhe a regra pela porta das traseiras.
func (h Training) Events(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		apierr.Write(w, apierr.ValidationFailed, "Sessão desconhecida.", "id")
		return
	}

	var req dto.SessionEventsRequest
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if len(req.Events) == 0 {
		apierr.Write(w, apierr.ValidationFailed, "Sem eventos para gravar.", "events")
		return
	}

	day, err := time.Parse("2006-01-02", req.LocalDay)
	if err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Dia inválido.", "localDay")
		return
	}

	eventos := make([]service.SessionEvent, 0, len(req.Events))
	for _, e := range req.Events {
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			// Um evento sem instante não se ordena nem se deduplica. Recusa-se o
			// evento, não o lote: perder trinta séries por causa de uma é pior.
			continue
		}
		eventos = append(eventos, service.SessionEvent{
			Kind: e.Type, StepIndex: e.Index, Payload: e.Payload, At: at,
		})
	}

	out, err := h.Service.AppendEvents(r.Context(), userID, sessionID, eventos, day)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		apierr.Write(w, apierr.NotFound, "Essa sessão não existe.", "id")
		return
	case err != nil:
		apierr.WriteInternal(w, r, err, "Não foi possível gravar o treino.")
		return
	}

	apierr.WriteJSON(w, http.StatusOK, dto.SessionEventsResponse{
		Accepted: out.Accepted, Duplicates: out.Duplicates,
		Ended: out.Ended, Status: out.Status,
		CountsForStreak: out.CountsForStreak, Streak: out.Streak,
	})
}

/*
 * aplicarFeito põe o que aconteceu em cada série, por cima do que foi pedido.
 *
 * Casa por slug de exercício e por índice de série. O que o cliente não mandar
 * fica sem `actual` — e é isso que se quer: uma série sem detalhe é uma série
 * de que não se sabe o detalhe, não uma série igual ao alvo. Escrever o alvo
 * como se fosse o feito era inventar que toda a gente cumpre o plano à letra.
 */
func aplicarFeito(prescricoes []repo.PrescriptionRow, feito []dto.PerformedExercise) {
	if len(feito) == 0 || len(prescricoes) == 0 {
		return
	}
	porSlug := make(map[string]*repo.PrescriptionRow, len(prescricoes))
	for i := range prescricoes {
		porSlug[prescricoes[i].ExerciseSlug] = &prescricoes[i]
	}

	for _, e := range feito {
		p, ok := porSlug[e.ExerciseID]
		if !ok {
			// Um exercício que não estava no plano não tem onde encaixar. Cai
			// em silêncio: o cliente pode estar uma versão à frente.
			continue
		}
		for _, s := range e.Sets {
			if s.Index < 0 || s.Index >= len(p.Detail) {
				continue
			}
			m, ok := metricaDoFeito(s)
			if !ok {
				continue
			}
			p.Detail[s.Index].Actual = &m
			p.Detail[s.Index].Completed = s.Completed
		}
	}
}

// metricaDoFeito escolhe a forma pela combinação que veio, da mais específica
// para a mais simples. Sem nenhuma, não há métrica — e não se inventa uma.
func metricaDoFeito(s dto.PerformedSet) (domain.Metric, bool) {
	switch {
	case s.Reps != nil && s.WeightKg != nil:
		return domain.LoadReps(*s.Reps, *s.WeightKg), true
	case s.DistanceMeters != nil:
		return domain.Meters(*s.DistanceMeters), true
	case s.Reps != nil:
		return domain.Reps(*s.Reps), true
	case s.DurationSeconds != nil:
		return domain.Seconds(*s.DurationSeconds), true
	default:
		return domain.Metric{}, false
	}
}

/*
 * porExtenso escreve uma métrica como ela se lê: "10 × 40 kg", "45 s", "400 m".
 *
 * É o servidor a escrever, e não o ecrã, pela mesma razão das etiquetas das
 * aulas: o cliente não tem de saber que `load_reps` leva um "×" no meio e que a
 * distância leva "m" no fim. Quem tem os dados escreve a frase.
 */
func porExtenso(m domain.Metric) string {
	switch m.Type {
	case domain.MetricLoadReps:
		if m.Reps == nil || m.WeightKg == nil {
			return ""
		}
		return fmt.Sprintf("%d × %s kg", *m.Reps, semZerosAtras(*m.WeightKg))
	case domain.MetricReps:
		if m.Reps == nil {
			return ""
		}
		return fmt.Sprintf("%d reps", *m.Reps)
	case domain.MetricTime:
		if m.DurationSeconds == nil {
			return ""
		}
		return fmt.Sprintf("%d s", *m.DurationSeconds)
	case domain.MetricDistance:
		if m.DistanceMeters == nil {
			return ""
		}
		return fmt.Sprintf("%d m", *m.DistanceMeters)
	default:
		return ""
	}
}

/*
 * semZerosAtras escreve 40 e não 40,0 — e escreve 42,5 com vírgula.
 *
 * A vírgula não é um detalhe: a app fala português do princípio ao fim e
 * mostra "78,0 kg" no peso. Um "42.5" no meio disso é a costura a aparecer.
 */
func semZerosAtras(v float64) string {
	return strings.ReplaceAll(strconv.FormatFloat(v, 'f', -1, 64), ".", ",")
}

/*
 * LoadSuggestions propõe a carga de hoje a partir do que ficou registado.
 *
 * ⚠️ **Propõe; não manda.** O número chega ao comando já preenchido e quem
 * treina muda-o com um toque. A app sabe o que ficou registado — não sabe se a
 * pessoa dormiu mal, nem se a barra é a mesma.
 */
func (h Training) LoadSuggestions(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Sessions == nil {
		apierr.Write(w, apierr.Internal, "O histórico está indisponível.", "")
		return
	}

	ultimas, err := h.Sessions.LastLoads(r.Context(), userID)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as cargas anteriores.")
		return
	}

	out := make([]map[string]any, 0, len(ultimas))
	for _, u := range ultimas {
		p := training.PropoeCarga(u.ExerciseSlug, u.WeightKg, u.Completas)
		if p.SugestaoKg <= 0 {
			// Sem proposta não se manda linha: uma lista com números a zero é
			// uma lista que o ecrã tem de filtrar outra vez.
			continue
		}
		out = append(out, map[string]any{
			"exerciseId": p.ExerciseID,
			"lastKg":     p.UltimaKg,
			"suggestKg":  p.SugestaoKg,
			"reason":     p.Motivo,
			"lastDay":    u.LocalDay.Format("2006-01-02"),
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}
