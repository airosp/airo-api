package handlers

import (
	"context"
	"net/http"
	"time"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// MeasurementStore lê e escreve a série das medições.
type MeasurementStore interface {
	Series(ctx context.Context, userID, metric string, since time.Time) ([]repo.MeasurementEntry, error)
	AddMeasurement(ctx context.Context, userID string, m repo.MeasurementEntry) (string, error)
}

/*
 * Measurements serve a série do corpo: peso, gordura, perímetros.
 *
 * ⚠️ **O peso é uma série, não um campo.** Existia só como número no perfil e
 * como tendência já calculada no `/progress/snapshot` — ou seja, o servidor
 * sabia para onde a pessoa ia e não sabia dizer-lhe os pontos por onde passou.
 * O ecrã de evolução guardava-os no telemóvel, e uma reinstalação apagava-os.
 */
type Measurements struct{ Store MeasurementStore }

// As métricas que se podem ler e escrever por aqui.
//
// A lista existe para o `metric` não entrar cru num enum do Postgres: um valor
// desconhecido rebentava a consulta com um erro de conversão em vez de uma
// resposta que se percebe.
var metricasValidas = map[string]bool{
	"body_weight": true, "body_fat": true,
	"waist": true, "chest": true, "hip": true, "thigh": true, "arm": true,
	"resting_hr": true,
}

/** Quanto se lê por omissão: dois anos chegam para qualquer gráfico da app. */
const janelaDeMedicoes = 730 * 24 * time.Hour

// List devolve a série, da mais recente para a mais antiga.
func (h Measurements) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As medições estão indisponíveis.", "")
		return
	}

	metrica := r.URL.Query().Get("metric")
	if metrica == "" {
		metrica = "body_weight"
	}
	if !metricasValidas[metrica] {
		apierr.Write(w, apierr.ValidationFailed, "Métrica desconhecida.", "metric")
		return
	}

	desde := time.Now().Add(-janelaDeMedicoes)
	if v := r.URL.Query().Get("since"); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			apierr.Write(w, apierr.ValidationFailed, "Data inválida.", "since")
			return
		}
		desde = d
	}

	serie, err := h.Store.Series(r.Context(), userID, metrica, desde)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as medições.")
		return
	}

	out := make([]map[string]any, 0, len(serie))
	for _, m := range serie {
		linha := map[string]any{
			"id": m.ID, "metric": m.Metric, "value": m.Value, "unit": m.Unit,
			"recordedAt": m.RecordedAt.UTC().Format(time.RFC3339),
			// O dia local por extenso: é por dia que a lista agrupa, e deixar o
			// cliente cortar a data do carimbo era deixá-lo cortá-la no fuso
			// errado.
			"day": m.RecordedAt.UTC().Format("2006-01-02"),
		}
		if m.Note != "" {
			linha["note"] = m.Note
		}
		out = append(out, linha)
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"measurements": out})
}

// Add acrescenta um ponto à série.
//
// Repetir o mesmo ponto — mesma métrica, mesmo dia, mesmo valor — devolve 200 e
// não cria nada. É o que permite ao telemóvel reenviar o que ficou por enviar
// sem rede, sem duplicar a lista de quem escreveu o peso uma vez só.
func (h Measurements) Add(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Store == nil {
		apierr.Write(w, apierr.Internal, "As medições estão indisponíveis.", "")
		return
	}

	var req struct {
		Metric     string  `json:"metric"`
		Value      float64 `json:"value"`
		Unit       string  `json:"unit"`
		RecordedAt string  `json:"recordedAt"`
		Note       string  `json:"note"`
	}
	if err := decode(r, &req); err != nil {
		apierr.Write(w, apierr.ValidationFailed, "Corpo do pedido inválido.", "")
		return
	}
	if req.Metric == "" {
		req.Metric = "body_weight"
	}
	if !metricasValidas[req.Metric] {
		apierr.Write(w, apierr.ValidationFailed, "Métrica desconhecida.", "metric")
		return
	}
	if req.Value <= 0 {
		apierr.Write(w, apierr.ValidationFailed, "O valor tem de ser positivo.", "value")
		return
	}
	if req.Unit == "" {
		req.Unit = unidadeDe(req.Metric)
	}

	quando := time.Now().UTC()
	if req.RecordedAt != "" {
		t, err := time.Parse(time.RFC3339, req.RecordedAt)
		if err != nil {
			apierr.Write(w, apierr.ValidationFailed, "Data inválida.", "recordedAt")
			return
		}
		quando = t.UTC()
	}

	id, err := h.Store.AddMeasurement(r.Context(), userID, repo.MeasurementEntry{
		Metric: req.Metric, Value: req.Value, Unit: req.Unit,
		RecordedAt: quando, Note: req.Note,
	})
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível gravar a medição.")
		return
	}

	// Sem `id` quer dizer que o ponto já lá estava — 200, não 201: nada nasceu.
	estado := http.StatusCreated
	if id == "" {
		estado = http.StatusOK
	}
	apierr.WriteJSON(w, estado, map[string]any{
		"id": id, "metric": req.Metric, "value": req.Value, "unit": req.Unit,
		"recordedAt": quando.Format(time.RFC3339),
		"day":        quando.Format("2006-01-02"),
		"created":    id != "",
	})
}

func unidadeDe(metrica string) string {
	switch metrica {
	case "body_weight":
		return "kg"
	case "body_fat":
		return "%"
	case "resting_hr":
		return "bpm"
	default:
		return "cm"
	}
}
