package mux

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

/*
 * O webhook do Mux — como se sabe que um vídeo ficou pronto.
 *
 * Carregar um vídeo não o torna reproduzível: o Mux recebe, transcodifica em
 * vários débitos e só depois emite o identificador de reprodução. Isso demora
 * de segundos a minutos, e não há como saber quando acaba sem perguntar em
 * ciclo — ou sem ser avisado.
 *
 * ⚠️ **A notificação autentica-se pela assinatura, não pela sessão.** Quem
 * chama é um servidor do Mux, que não tem conta na Airo. O cabeçalho
 * `Mux-Signature` traz `t=<segundos>,v1=<hmac>`, e o que é assinado é
 * `t.corpo` — o tempo, um ponto, e o corpo **em bruto**. Reserializar o JSON
 * antes de verificar muda um espaço e deita a assinatura abaixo; é por isso
 * que quem verifica recebe os bytes como chegaram.
 */

// ErrAssinatura — a notificação não veio do Mux, ou veio adulterada.
var ErrAssinatura = errors.New("mux: assinatura inválida")

// ErrAtrasada — a notificação é velha de mais para se aceitar.
//
// Sem esta janela, quem apanhasse uma notificação legítima podia reenviá-la
// para sempre: a assinatura continua válida, porque nada nela envelhece.
var ErrAtrasada = errors.New("mux: notificação fora da janela de tempo")

// ToleranciaWebhook é a janela em que uma notificação ainda se aceita.
const ToleranciaWebhook = 5 * time.Minute

/*
VerificarAssinatura confere o cabeçalho `Mux-Signature` sobre o corpo em bruto.

Sem segredo configurado devolve erro em vez de deixar passar: um webhook que
aceita tudo é uma porta aberta para qualquer pessoa marcar aulas como prontas,
ou apontá-las a vídeos que não são nossos.
*/
func VerificarAssinatura(cabecalho string, corpo []byte, segredo string, agora time.Time) error {
	if segredo == "" {
		return fmt.Errorf("%w: sem segredo configurado", ErrAssinatura)
	}

	var ts, v1 string
	for _, parte := range strings.Split(cabecalho, ",") {
		chave, valor, ok := strings.Cut(strings.TrimSpace(parte), "=")
		if !ok {
			continue
		}
		switch chave {
		case "t":
			ts = valor
		case "v1":
			v1 = valor
		}
	}
	if ts == "" || v1 == "" {
		return fmt.Errorf("%w: cabeçalho incompleto", ErrAssinatura)
	}

	segundos, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: tempo ilegível", ErrAssinatura)
	}
	if delta := agora.Sub(time.Unix(segundos, 0)); delta > ToleranciaWebhook || delta < -ToleranciaWebhook {
		return ErrAtrasada
	}

	mac := hmac.New(sha256.New, []byte(segredo))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(corpo)
	esperado := hex.EncodeToString(mac.Sum(nil))

	// `hmac.Equal` e não `==`: comparar strings sai mais cedo à primeira
	// diferença, e o tempo que demora diz quantos caracteres estavam certos.
	if !hmac.Equal([]byte(esperado), []byte(v1)) {
		return ErrAssinatura
	}
	return nil
}

// Evento é o que interessa de uma notificação do Mux.
type Evento struct {
	Tipo  string `json:"type"`
	Dados struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		// Passthrough é o que lhe pedimos para nos devolver: o id da aula.
		// Sem ele, um recurso pronto não sabe a que aula pertence.
		Passthrough string  `json:"passthrough"`
		Duration    float64 `json:"duration"`
		PlaybackIDs []struct {
			ID     string `json:"id"`
			Policy string `json:"policy"`
		} `json:"playback_ids"`
		// As dimensões chegam na faixa de vídeo, e servem para o cliente
		// reservar o espaço certo antes de carregar.
		Tracks []struct {
			Type         string  `json:"type"`
			MaxWidth     int     `json:"max_width"`
			MaxHeight    int     `json:"max_height"`
			MaxFrameRate float64 `json:"max_frame_rate"`
		} `json:"tracks"`
	} `json:"data"`
}

// Playback devolve o identificador e a política do recurso.
func (e Evento) Playback() (id, politica string, ok bool) {
	for _, p := range e.Dados.PlaybackIDs {
		if p.ID != "" {
			return p.ID, p.Policy, true
		}
	}
	return "", "", false
}

// Dimensoes devolve a largura e a altura da faixa de vídeo, se vierem.
func (e Evento) Dimensoes() (largura, altura int, ok bool) {
	for _, t := range e.Dados.Tracks {
		if t.Type == "video" && t.MaxWidth > 0 && t.MaxHeight > 0 {
			return t.MaxWidth, t.MaxHeight, true
		}
	}
	return 0, 0, false
}

// LerEvento decifra o corpo já verificado.
func LerEvento(corpo []byte) (Evento, error) {
	var e Evento
	if err := json.Unmarshal(corpo, &e); err != nil {
		return Evento{}, fmt.Errorf("mux: notificação ilegível: %w", err)
	}
	return e, nil
}
