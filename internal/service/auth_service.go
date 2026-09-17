package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// Sender entrega o código. WhatsApp em produção; nos testes, um duplo.
type Sender interface {
	Send(ctx context.Context, phone, code, channel string) (messageID string, err error)
}

// Undeliverable distingue "este número não recebe por aqui" de "falhou agora".
// São coisas diferentes para quem está a tentar entrar: a primeira não melhora
// com uma segunda tentativa.
//
// É uma interface e não um erro concreto para o serviço não ter de conhecer o
// canal — nem o canal o serviço.
type Undeliverable interface {
	Undeliverable() bool
}

type AuthConfig struct {
	Pepper []byte

	CodeTTL     time.Duration
	MaxAttempts int
	ResendAfter time.Duration
	RefreshTTL  time.Duration
	/*
	 * SessionMaxAge é quanto tempo uma sessão vive **desde que foi aberta**,
	 * por mais que se renove.
	 *
	 * ⚠️ O `RefreshTTL` não chega: cada rotação emite um token com prazo novo,
	 * por isso quem abre a app todas as semanas nunca mais volta a provar que o
	 * número é dele. E os números de telemóvel são reciclados pelas operadoras
	 * — em Moçambique, um cartão sem uso volta ao mercado. Sem tecto, a conta
	 * fica acessível a quem herdar o número, com o histórico de peso, as
	 * medidas e o nome de quem a abriu.
	 */
	SessionMaxAge time.Duration

	Limits auth.Limits
}

func DefaultAuthConfig(pepper []byte) AuthConfig {
	return AuthConfig{
		Pepper: pepper,
		// Cinco minutos: curto o bastante para limitar, longo para trocar de app
		// e voltar.
		CodeTTL:     5 * time.Minute,
		MaxAttempts: 5,
		ResendAfter: 60 * time.Second,
		RefreshTTL:  60 * 24 * time.Hour,
		// Sessenta dias. Mais do que isso e o número pode já não ser da mesma
		// pessoa; menos, e pedia-se o código a quem treina todas as semanas.
		SessionMaxAge: 60 * 24 * time.Hour,
		Limits:        auth.DefaultLimits(),
	}
}

type AuthService struct {
	repo    *repo.AuthRepo
	limiter auth.Limiter
	sender  Sender
	cfg     AuthConfig
	clk     clock.Clock
}

func NewAuthService(r *repo.AuthRepo, l auth.Limiter, s Sender, cfg AuthConfig, clk clock.Clock) *AuthService {
	return &AuthService{repo: r, limiter: l, sender: s, cfg: cfg, clk: clk}
}

type RequestOTPInput struct {
	RawPhone string
	Channel  string
	DeviceID string
	IPHash   string
}

type RequestOTPResult struct {
	ChallengeID string
	ExpiresAt   time.Time
	ResendAfter int
}

var (
	ErrRateLimited = errors.New("limite excedido")
	/*
	 * ErrReauthRequired: a sessão passou da idade e é preciso provar o número
	 * outra vez.
	 *
	 * Distinto de um token inválido de propósito: aqui não houve roubo nem
	 * engano, e o ecrã diz porquê em vez de deixar a pessoa a pensar que a app
	 * se avariou.
	 */
	ErrReauthRequired = errors.New("sessão antiga: é preciso entrar outra vez")
	ErrDeliveryFailed = errors.New("entrega falhou")
	// ErrUndeliverable é o número que não recebe por este canal. Repetir não
	// resolve, e a mensagem a dar é outra.
	ErrUndeliverable = errors.New("número não alcançável por este canal")
	ErrOTPInvalid    = errors.New("código inválido")
	ErrOTPExpired    = errors.New("código expirado")
	ErrOTPExhausted  = errors.New("tentativas esgotadas")
)

// RequestOTP pede um código.
//
// ⚠️ **A resposta é exactamente a mesma para número registado e desconhecido.**
// E o utilizador **não** é criado aqui: criar é mais lento do que procurar, e se
// o registo demorasse sistematicamente mais, o tempo de resposta revelaria a
// informação que a resposta esconde.
func (s *AuthService) RequestOTP(ctx context.Context, in RequestOTPInput) (RequestOTPResult, error) {
	phone, region, err := auth.NormalizePhone(in.RawPhone, auth.DefaultRegion)
	if err != nil {
		return RequestOTPResult{}, err
	}
	_ = region

	decision, err := auth.Guard(ctx, s.limiter, s.cfg.Limits, auth.OTPRequest{
		PhoneE164: phone, CountryCode: phone[:4], IPHash: in.IPHash, DeviceID: in.DeviceID,
	})
	if err != nil {
		return RequestOTPResult{}, err
	}
	if !decision.Allowed {
		// O eixo vai para a auditoria, nunca para a resposta: dizer qual limite
		// bateu diz ao atacante o que contornar.
		_ = s.repo.AppendAuthEvent(ctx, "otp_rate_limited", nil, &phone, nil,
			map[string]any{"axis": decision.Axis})
		return RequestOTPResult{}, fmt.Errorf("%w: %v", ErrRateLimited, decision.RetryAfter)
	}

	code, err := auth.GenerateCode()
	if err != nil {
		return RequestOTPResult{}, err
	}
	hash, err := auth.HashCode(code, s.cfg.Pepper)
	if err != nil {
		return RequestOTPResult{}, err
	}

	channel := in.Channel
	if channel == "" || channel == "auto" {
		channel = "whatsapp"
	}

	var device *string
	if in.DeviceID != "" {
		d := in.DeviceID
		device = &d
	}

	now := s.clk.Now()
	id, err := s.repo.CreateChallenge(ctx, repo.Challenge{
		PhoneE164: phone, CodeHash: hash, Channel: channel, DeviceID: device,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.CodeTTL), MaxAttempts: s.cfg.MaxAttempts,
	})
	if err != nil {
		return RequestOTPResult{}, err
	}

	idDaMensagem, err := s.sender.Send(ctx, phone, code, channel)
	if err != nil {
		_ = s.repo.AppendAuthEvent(ctx, "otp_delivery_failed", nil, &phone, device, nil)

		var u Undeliverable
		if errors.As(err, &u) && u.Undeliverable() {
			// Aqui o orçamento **fica gasto**. O número não recebe por este
			// canal, e isso é informação sobre o pedido: sem custo, bastava
			// repetir contra números sem WhatsApp para gastar a nossa quota na
			// Meta à vontade.
			return RequestOTPResult{}, fmt.Errorf("%w: %v", ErrUndeliverable, err)
		}

		// A falha é nossa — configuração, rede, a Meta em baixo. Quem pediu não
		// fez nada de errado e não recebeu mensagem nenhuma: devolve-se o que
		// o pedido consumiu.
		//
		// Sem isto, um template mal configurado esgotou cinco de cinco
		// tentativas de alguém e deixou-o de fora vinte horas, sem uma única
		// mensagem enviada. O castigo era inteiramente por um erro nosso.
		auth.Refund(ctx, s.limiter, decision)

		return RequestOTPResult{}, fmt.Errorf("%w: %v", ErrDeliveryFailed, err)
	}
	// O identificador que a Meta devolve é o que liga este desafio ao webhook
	// de entrega: sem ele guardado, a confirmação chega e não se sabe de quem é.
	if idDaMensagem != "" {
		_ = s.repo.SetChallengeMessageID(ctx, id, idDaMensagem)
	}
	_ = s.repo.AppendAuthEvent(ctx, "otp_requested", nil, &phone, device, map[string]any{"channel": channel})

	return RequestOTPResult{
		ChallengeID: id,
		ExpiresAt:   now.Add(s.cfg.CodeTTL),
		ResendAfter: int(s.cfg.ResendAfter.Seconds()),
	}, nil
}

type VerifyOTPInput struct {
	ChallengeID string
	Code        string
	DeviceID    string
	Platform    string
}

type VerifyOTPResult struct {
	UserID       string
	RefreshToken string
	// IsNewUser só aparece **depois** de o código ser verificado: nessa altura a
	// pessoa já provou controlar o número.
	IsNewUser bool
}

// VerifyOTP confirma o código e, se for a primeira vez, cria a conta.
func (s *AuthService) VerifyOTP(ctx context.Context, in VerifyOTPInput) (VerifyOTPResult, error) {
	var out VerifyOTPResult
	var failed bool
	var challengeID, phone string

	err := s.repo.WithTx(ctx, func(ctx context.Context) error {
		challenge, err := s.repo.Challenge(ctx, in.ChallengeID)
		if err != nil {
			return ErrOTPInvalid
		}
		challengeID, phone = challenge.ID, challenge.PhoneE164

		switch challenge.Status {
		case "exhausted":
			return ErrOTPExhausted
		case "verified", "superseded":
			return ErrOTPInvalid
		}
		if s.clk.Now().After(challenge.ExpiresAt) {
			return ErrOTPExpired
		}

		if !auth.VerifyCode(in.Code, challenge.CodeHash, s.cfg.Pepper) {
			// ⚠️ A contagem é **fora** desta transacção.
			//
			// Contá-la aqui dentro e devolver erro a seguir faz a transacção
			// reverter — e a contagem com ela. As tentativas ficavam sempre a
			// zero e as cinco de limite nunca chegavam: a força bruta tinha um
			// milhão de tentativas, não cinco.
			failed = true
			return ErrOTPInvalid
		}

		if err := s.repo.MarkVerified(ctx, challenge.ID); err != nil {
			return err
		}

		userID, existed, err := s.repo.UserByPhone(ctx, challenge.PhoneE164)
		if err != nil {
			return err
		}
		if !existed {
			_, region, _ := auth.NormalizePhone(challenge.PhoneE164, auth.DefaultRegion)
			userID, err = s.repo.CreateUser(ctx, challenge.PhoneE164, region)
			if err != nil {
				return err
			}
		}
		out.UserID, out.IsNewUser = userID, !existed

		platform := in.Platform
		if platform == "" {
			platform = "ios"
		}
		if err := s.repo.TouchDevice(ctx, in.DeviceID, userID, platform); err != nil {
			return err
		}

		token, err := s.issueRefresh(ctx, userID, in.DeviceID, "")
		if err != nil {
			return err
		}
		out.RefreshToken = token

		return s.repo.AppendAuthEvent(ctx, "login", &userID, &challenge.PhoneE164, &in.DeviceID,
			map[string]any{"isNewUser": out.IsNewUser})
	})

	if failed {
		// Fora da transacção revertida: a contagem persiste, que é o que a
		// torna uma defesa.
		attempts, countErr := s.repo.CountAttempt(ctx, challengeID)
		if countErr != nil {
			return out, countErr
		}
		_ = s.repo.AppendAuthEvent(ctx, "otp_failed", nil, &phone, nil,
			map[string]any{"attempts": attempts})
		if attempts >= s.cfg.MaxAttempts {
			return out, ErrOTPExhausted
		}
		// ⚠️ A mensagem **não diz quantas tentativas faltam**: isso diz ao
		// atacante exactamente quanto orçamento lhe resta.
		return out, ErrOTPInvalid
	}
	return out, err
}

// issueRefresh emite um token opaco. Opaco e não JWT: tem de ser revogável, e um
// JWT só é revogável com uma lista de revogados — que é uma tabela na mesma, com
// a desvantagem de parecer que não é precisa.
func (s *AuthService) issueRefresh(ctx context.Context, userID, deviceID, familyID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))

	agora := s.clk.Now()
	if _, _, err := s.repo.InsertRefresh(ctx, userID, deviceID, familyID, sum[:],
		agora, agora.Add(s.cfg.RefreshTTL)); err != nil {
		return "", err
	}
	return token, nil
}

type RefreshResult struct {
	UserID       string
	RefreshToken string
}

// Refresh roda o token e **detecta reutilização**.
//
// Cada refresh devolve um novo e invalida o anterior. Se um token já usado
// voltar a aparecer, foi roubado: revoga-se a família inteira. Sem isto, quem
// copia um refresh fica com acesso indefinido sem que se note.
// Logout termina a sessão deste aparelho.
//
// Revoga a **família**, não a linha. A família nasce no verify e é por
// aparelho; a rotação mantém-na. Revogar só a linha deixaria de pé um token
// que a rotação tivesse emitido no milissegundo anterior — e "terminei sessão"
// passaria a querer dizer "quase".
//
// ⚠️ **Responde sempre igual**, conhecido ou não, já revogado ou não. Dizer
// "esse token não existe" dá a quem tenta um oráculo para saber quais existem.
// E torna a operação repetível: sair duas vezes é sair.
func (s *AuthService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	rt, err := s.repo.RefreshByHash(ctx, sum[:])
	if err != nil {
		// Token desconhecido. Nada a revogar, e nada a dizer.
		return nil
	}
	if rt.RevokedAt != nil {
		return nil
	}

	if err := s.repo.RevokeFamily(ctx, rt.FamilyID, "logout"); err != nil {
		return fmt.Errorf("revogar sessão: %w", err)
	}
	// Fora de qualquer transação que possa falhar: um registo de auditoria que
	// desaparece com o erro seguinte é um registo que mente.
	_ = s.repo.AppendAuthEvent(ctx, "logout", &rt.UserID, nil, &rt.DeviceID, nil)
	return nil
}

func (s *AuthService) Refresh(ctx context.Context, token, deviceID string) (RefreshResult, error) {
	var out RefreshResult
	var reuse *repo.RefreshToken
	// A sessão que passou da idade. Revoga-se **fora** da transacção, pela mesma
	// razão que a reutilização: revogar aqui e devolver erro a seguir faz a
	// transacção reverter, e a família ficava viva.
	var velha *repo.RefreshToken
	sum := sha256.Sum256([]byte(token))

	err := s.repo.WithTx(ctx, func(ctx context.Context) error {
		existing, err := s.repo.RefreshByHash(ctx, sum[:])
		if err != nil {
			return repo.ErrTokenNotFound
		}

		if existing.RevokedAt != nil {
			// Já foi usado: foi roubado.
			//
			// ⚠️ A revogação é **fora** desta transacção, pela mesma razão que a
			// contagem de tentativas: revogar aqui e devolver erro a seguir faz
			// a transacção reverter, e a família ficava viva. O atacante
			// continuava lá dentro, e o registo diria que tinha sido expulso.
			t := existing
			reuse = &t
			return repo.ErrTokenReuse
		}
		if s.clk.Now().After(existing.ExpiresAt) {
			return repo.ErrTokenNotFound
		}

		// A sessão tem idade, e não só prazo: ver `SessionMaxAge`.
		if s.cfg.SessionMaxAge > 0 {
			desde, err := s.repo.FamilyStartedAt(ctx, existing.FamilyID)
			if err != nil {
				return err
			}
			if s.clk.Now().Sub(desde) > s.cfg.SessionMaxAge {
				t := existing
				velha = &t
				return ErrReauthRequired
			}
		}

		next, err := s.issueRefresh(ctx, existing.UserID, existing.DeviceID, existing.FamilyID)
		if err != nil {
			return err
		}
		nextSum := sha256.Sum256([]byte(next))
		created, err := s.repo.RefreshByHash(ctx, nextSum[:])
		if err != nil {
			return err
		}
		if err := s.repo.ReplaceRefresh(ctx, existing.ID, created.ID); err != nil {
			return err
		}
		out.UserID, out.RefreshToken = existing.UserID, next
		return nil
	})

	if velha != nil {
		if err := s.repo.RevokeFamily(ctx, velha.FamilyID, "reauth_required"); err != nil {
			return out, err
		}
		_ = s.repo.AppendAuthEvent(ctx, "reauth_required", &velha.UserID, nil, &velha.DeviceID, nil)
		return out, ErrReauthRequired
	}
	if reuse != nil {
		// Não se sabe qual das duas partes é a legítima, e deixar as duas a
		// correr é deixar o atacante lá dentro. Cai tudo, e a pessoa volta a
		// entrar.
		if err := s.repo.RevokeFamily(ctx, reuse.FamilyID, "reuse_detected"); err != nil {
			return out, err
		}
		_ = s.repo.AppendAuthEvent(ctx, "token_reuse_detected", &reuse.UserID, nil, &reuse.DeviceID, nil)
		return out, repo.ErrTokenReuse
	}
	return out, err
}
