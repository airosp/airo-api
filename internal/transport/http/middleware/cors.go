package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CORS responde ao pedido prévio do browser e autoriza as origens conhecidas.
//
// No telemóvel isto não existe — o `fetch` nativo não faz pedido prévio. Existe
// porque a app também corre na web (react-native-web), e sem isto o browser
// recusa antes de a API chegar a ver o pedido: o ecrã diz "sem ligação" e o
// servidor não regista nada, porque nada lhe chegou.
//
// A lista é fechada de propósito. `*` devolvido a toda a gente é o mesmo que
// não ter política, e um dia alguém acrescenta credenciais por cookie e a
// permissão passa a valer o que não devia.
func CORS(allowed []string) func(http.Handler) http.Handler {
	index := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		if o = strings.TrimSpace(strings.TrimSuffix(o, "/")); o != "" {
			index[o] = true
		}
	}

	const headers = "Authorization, Content-Type, Idempotency-Key, X-Request-Id, X-Device-Id"
	// Sem isto o JS não consegue ler estes dois: o browser só deixa ver a
	// lista segura, e `Retry-After` não está nela. O ecrã dizia "tenta daqui a
	// pouco" sem saber quanto — e o contador ficava mudo.
	const exposed = "Retry-After, X-Request-Id"
	// Esta lista tem de cobrir **todos** os métodos que o router regista. Ficou
	// sem `PUT` durante meia hora depois de `PUT /v1/profile` nascer, e o
	// sintoma no browser foi "sem ligação": o pedido prévio é recusado e o
	// `fetch` rejeita sem estado nenhum. `TestCORSCobreTodosOsMetodos` compara
	// as duas listas.
	const methods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	maxAge := strconv.Itoa(int((24 * time.Hour).Seconds()))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSuffix(r.Header.Get("Origin"), "/")

			if origin != "" && index[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				// Sem isto, uma cache intermédia serve a resposta de uma origem
				// a outra — e a autorização passa a depender de quem chegou
				// primeiro.
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				w.Header().Set("Access-Control-Expose-Headers", exposed)
				w.Header().Set("Access-Control-Max-Age", maxAge)
			}

			// O pedido prévio termina aqui: não passa aos handlers, e uma
			// origem desconhecida recebe 204 **sem** os cabeçalhos — que é o
			// que faz o browser recusar, sem lhe dar um erro para distinguir
			// origem recusada de rota inexistente.
			if r.Method == http.MethodOptions &&
				r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
