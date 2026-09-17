package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/service"
)

/*
 * A sessão tem idade, e não só prazo.
 *
 * ⚠️ Cada renovação emitia um token com sessenta dias novos, por isso quem
 * abrisse a app todas as semanas nunca mais voltava a provar que o número era
 * seu. E os números são reciclados pelas operadoras: sem tecto, a conta — com
 * o peso, as medidas e o nome — fica acessível a quem herdar o número.
 */
func TestSessaoVelhaPedeOCodigoOutraVez(t *testing.T) {
	svc, sender, _, relogio := authSetup(t)
	ctx := context.Background()

	refresh := entrar(t, svc, sender, "+258841110001")

	// Dois meses de renovações semanais: a sessão mantém-se de pé.
	for semana := 1; semana <= 8; semana++ {
		relogio.Advance(7 * 24 * time.Hour)
		out, err := svc.Refresh(ctx, refresh, aparelho)
		if err != nil {
			t.Fatalf("semana %d: %v", semana, err)
		}
		refresh = out.RefreshToken
	}

	// Passados os sessenta dias desde a **entrada**, pede-se o código outra vez.
	relogio.Advance(5 * 24 * time.Hour)
	if _, err := svc.Refresh(ctx, refresh, aparelho); !errors.Is(err, service.ErrReauthRequired) {
		t.Fatalf("esperava ErrReauthRequired, veio %v", err)
	}
}

// Antes do prazo, renovar é renovar — e não se pede nada a ninguém.
func TestAntesDoPrazoARenovacaoPassa(t *testing.T) {
	svc, sender, _, relogio := authSetup(t)
	ctx := context.Background()

	refresh := entrar(t, svc, sender, "+258841110002")
	relogio.Advance(59 * 24 * time.Hour)

	if _, err := svc.Refresh(ctx, refresh, aparelho); err != nil {
		t.Fatalf("aos 59 dias: %v", err)
	}
}

/*
 * Passada a idade, a família cai toda.
 *
 * Deixar de pé o token que a rotação emitiu no milissegundo anterior era deixar
 * a porta aberta e dizer que se tinha fechado.
 */
func TestPassadaAIdadeAFamiliaCai(t *testing.T) {
	svc, sender, _, relogio := authSetup(t)
	ctx := context.Background()

	refresh := entrar(t, svc, sender, "+258841110003")
	// Renovações semanais: cada uma emite um token com prazo novo, por isso o
	// prazo do token nunca acaba. O que acaba é a idade da sessão.
	for semana := 1; semana <= 9; semana++ {
		relogio.Advance(7 * 24 * time.Hour)
		out, err := svc.Refresh(ctx, refresh, aparelho)
		if err != nil {
			if semana < 9 {
				t.Fatalf("semana %d: %v", semana, err)
			}
			if !errors.Is(err, service.ErrReauthRequired) {
				t.Fatalf("semana %d: veio %v", semana, err)
			}
			break
		}
		refresh = out.RefreshToken
	}
	// E a segunda tentativa também não passa — a família está revogada, e o
	// token revogado não pode parecer roubo: já foi expulso por prazo.
	if _, err := svc.Refresh(ctx, refresh, aparelho); err == nil {
		t.Fatal("o token continuou a servir")
	}

	// Entrar outra vez funciona, e a sessão nova é nova.
	novo := entrar(t, svc, sender, "+258841110003")
	if _, err := svc.Refresh(ctx, novo, aparelho); err != nil {
		t.Fatalf("depois de entrar outra vez: %v", err)
	}
}

/** O aparelho de onde se entra nestes testes. */
const aparelho = "aparelho-de-ensaio"

/** Entra e devolve o refresh. É o começo de todas as histórias daqui. */
func entrar(t *testing.T, svc *service.AuthService, sender *capturingSender, telefone string) string {
	t.Helper()
	ctx := context.Background()
	desafio, err := svc.RequestOTP(ctx, service.RequestOTPInput{
		RawPhone: telefone, Channel: "auto", DeviceID: aparelho, IPHash: "hash-ip",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: desafio.ChallengeID, Code: sender.code(telefone),
		DeviceID: aparelho, Platform: "ios",
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.RefreshToken
}
