package handlers

import (
	"net/http"
	"time"
)

/*
 * A sessão da web num cookie que o script não lê.
 *
 * ⚠️ **Na web o refresh vive em memória** (D7), e recarregar a página é sair.
 * A alternativa que D7 já nomeava é esta: um cookie `httpOnly`, que é trabalho
 * do servidor. `localStorage` foi recusado por ser texto simples ao alcance de
 * qualquer script da página — e um cookie `httpOnly` é o contrário disso: o
 * browser envia-o e nenhum script o consegue ler.
 *
 * ⚠️ **`SameSite=None`, e não por descuido.** A app web vive em
 * `app.airo.co.mz` e a API em `airo-api.savanapoint.com` — **sites
 * diferentes**. Um cookie `Strict` ou `Lax` nunca sairia do browser para a
 * API, e a funcionalidade não funcionaria de todo. Com `None`, o cookie viaja
 * em pedidos de outros sites — e é por isso que o CSRF deixa de ser opcional.
 *
 * A defesa é o **cabeçalho obrigatório**. Um formulário de outro site não
 * consegue pôr cabeçalhos, e um `fetch` com um cabeçalho fora da lista simples
 * obriga o browser a pedir autorização prévia — que esta API só dá às origens
 * da sua lista. Quem não estiver na lista não consegue sequer fazer o pedido.
 *
 * Sem isto, o pior que um site hostil conseguiria era **rodar** o token da
 * vítima: não leria a resposta (o CORS recusa-lhe a origem), mas a rotação
 * forçada dispara a detecção de reutilização e deita a sessão abaixo. Um
 * encerramento de sessão à distância é um ataque, mesmo sem roubo.
 */

// NomeDoCookieDeSessao é o cookie do refresh na web.
const NomeDoCookieDeSessao = "airo_refresh"

/*
 * CabecalhoDeCliente é o que prova que o pedido veio de um `fetch` autorizado.
 *
 * O valor não interessa — o que interessa é que **exista**. Pô-lo obriga o
 * browser a pedir autorização prévia, e é aí que a lista de origens decide.
 */
const CabecalhoDeCliente = "X-Airo-Client"

/*
 * PorCookie decide se esta resposta usa o cookie em vez do corpo.
 *
 * Duas condições, e as duas são necessárias: a funcionalidade tem de estar
 * ligada, e o pedido tem de trazer o cabeçalho. Um cliente nativo não o manda
 * e continua a receber o refresh no corpo, como sempre.
 */
func PorCookie(ligado bool, r *http.Request) bool {
	return ligado && r.Header.Get(CabecalhoDeCliente) != ""
}

/*
 * PorCookieDeSessao põe o refresh num cookie que o script não lê.
 *
 * `Path` restrito às rotas de autenticação: um cookie que viaja em todos os
 * pedidos é um cookie que se perde num registo de acesso qualquer.
 */
func PorCookieDeSessao(w http.ResponseWriter, token string, duracao time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     NomeDoCookieDeSessao,
		Value:    token,
		Path:     "/v1/auth",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int(duracao.Seconds()),
	})
}

// LimparCookieDeSessao apaga-o. Os atributos têm de bater certo com os de
// cima, senão o browser guarda os dois e o antigo sobrevive à saída.
func LimparCookieDeSessao(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     NomeDoCookieDeSessao,
		Value:    "",
		Path:     "/v1/auth",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   -1,
	})
}

/*
 * RefreshDoPedido lê o refresh de onde ele vier: do corpo ou do cookie.
 *
 * O corpo ganha. Um cliente que o mande explicitamente está a dizer qual quer,
 * e um cookie esquecido de outra sessão não lhe deve passar à frente.
 */
func RefreshDoPedido(doCorpo string, r *http.Request) string {
	if doCorpo != "" {
		return doCorpo
	}
	if c, err := r.Cookie(NomeDoCookieDeSessao); err == nil {
		return c.Value
	}
	return ""
}
