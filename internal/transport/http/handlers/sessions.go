package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
	"github.com/airosp/airo-api/internal/transport/http/middleware"
)

// DeviceStore lê e termina as sessões dos aparelhos.
type DeviceStore interface {
	Sessions(ctx context.Context, userID string, agora time.Time) ([]repo.SessaoActiva, error)
	RevokeDevice(ctx context.Context, userID, deviceID, motivo string) (int, error)
}

/*
 * Sessions mostra onde a conta está aberta, e deixa fechar.
 *
 * ⚠️ O `deviceId` existia precisamente para isto e nunca teve rota. Quem
 * perdesse o telemóvel não tinha como o expulsar — e uma conta que não se
 * consegue fechar noutro sítio é uma conta que a pessoa não controla.
 */
type Sessions struct {
	Devices DeviceStore
	Clock   clock.Clock
}

var plataformaPorExtenso = map[string]string{
	"ios": "iPhone", "android": "Android", "web": "Browser",
}

func (h Sessions) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Devices == nil {
		apierr.Write(w, apierr.Internal, "As sessões estão indisponíveis.", "")
		return
	}

	agora := time.Now()
	if h.Clock != nil {
		agora = h.Clock.Now()
	}

	sessoes, err := h.Devices.Sessions(r.Context(), userID, agora)
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível ler as sessões.")
		return
	}

	/*
	 * Qual destes é "este" não se decide aqui.
	 *
	 * O `deviceId` não chega ao middleware — o token traz a marca, mas o
	 * verificador só devolve o utilizador. E não é preciso: quem sabe o seu
	 * identificador é o telemóvel, que o gerou na instalação. Mandar-lhe a
	 * lista e deixá-lo reconhecer-se é menos código dos dois lados do que
	 * alargar a interface de autenticação para responder a uma pergunta de
	 * ecrã.
	 */
	out := make([]map[string]any, 0, len(sessoes))
	for _, s := range sessoes {
		linha := map[string]any{
			"deviceId": s.DeviceID,
			"platform": s.Platform,
			"label":    etiquetaDeAparelho(s),
			"lastSeen": s.LastSeen.UTC().Format(time.RFC3339),
			"since":    s.FirstSeen.UTC().Format("2006-01-02"),
		}
		if s.Model != "" {
			linha["model"] = s.Model
		}
		if s.AppVersion != "" {
			linha["appVersion"] = s.AppVersion
		}
		out = append(out, linha)
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h Sessions) Revoke(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.Write(w, apierr.Unauthorized, "Sessão inválida ou expirada.", "")
		return
	}
	if h.Devices == nil {
		apierr.Write(w, apierr.Internal, "As sessões estão indisponíveis.", "")
		return
	}

	deviceID := r.PathValue("id")
	if deviceID == "" {
		apierr.Write(w, apierr.ValidationFailed, "Aparelho desconhecido.", "id")
		return
	}

	// `userID` no `WHERE` da revogação: sem ele, adivinhar um `deviceId`
	// expulsava outra pessoa da conta dela.
	caidos, err := h.Devices.RevokeDevice(r.Context(), userID, deviceID, "user_revoked")
	if err != nil {
		apierr.WriteInternal(w, r, err, "Não foi possível terminar a sessão.")
		return
	}
	if caidos == 0 {
		// Nada para terminar é o resultado que se queria. Não é erro.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/*
 * etiquetaDeAparelho escreve o que a pessoa reconhece.
 *
 * "iPhone 14 · v1.2" diz mais do que um `deviceId` de trinta caracteres, e é
 * por esse nome que alguém decide se aquela sessão é sua ou de um telemóvel que
 * já vendeu.
 */
func etiquetaDeAparelho(s repo.SessaoActiva) string {
	nome := plataformaPorExtenso[s.Platform]
	if nome == "" {
		nome = s.Platform
	}
	if s.Model != "" {
		nome = s.Model
	}
	if s.AppVersion != "" {
		return nome + " · v" + s.AppVersion
	}
	return nome
}
