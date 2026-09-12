package service_test

import (
	"context"
	"crypto/rand"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/auth"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// capturingSender guarda o código em vez de o enviar. É o que permite verificar
// o fluxo sem WhatsApp — e é também o único sítio do sistema onde o código em
// claro existe fora do dispositivo.
type capturingSender struct {
	mu    sync.Mutex
	codes map[string]string
	fail  bool
}

func newSender() *capturingSender { return &capturingSender{codes: map[string]string{}} }

func (s *capturingSender) Send(_ context.Context, phone, code, _ string) (string, error) {
	if s.fail {
		return "", errors.New("entrega falhou")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[phone] = code
	return "wamid.test", nil
}

func (s *capturingSender) code(phone string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codes[phone]
}

func authSetup(t *testing.T) (*service.AuthService, *capturingSender, *pgxpool.Pool, *clock.Fixed) {
	t.Helper()
	_, pool, _ := setup(t)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	pepper := make([]byte, 32)
	if _, err := rand.Read(pepper); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFixed(time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))
	sender := newSender()

	svc := service.NewAuthService(
		repo.NewAuthRepo(repo.NewTxManager(pool)),
		auth.NewRedisLimiter(rdb), sender,
		service.DefaultAuthConfig(pepper), clk)
	return svc, sender, pool, clk
}

// Um número diferente do que a fixture de `setup` cria: senão o teste que
// verifica que a conta **não** nasce no request contaria a conta da fixture.
const testPhone = "84 777 1234"
const testE164 = "+258847771234"

func requestOTP(t *testing.T, svc *service.AuthService, phone string) service.RequestOTPResult {
	t.Helper()
	out, err := svc.RequestOTP(context.Background(), service.RequestOTPInput{
		RawPhone: phone, Channel: "auto", DeviceID: "device-1", IPHash: "hash-ip",
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// T3.8/T3.9 — o fluxo completo, e a conta nasce no verify.
func TestFullOTPFlowCreatesUserOnVerify(t *testing.T) {
	svc, sender, pool, _ := authSetup(t)
	ctx := context.Background()

	challenge := requestOTP(t, svc, testPhone)
	if challenge.ChallengeID == "" || challenge.ResendAfter != 60 {
		t.Fatalf("%+v", challenge)
	}

	// ⚠️ A conta **ainda não existe**. Criar no request faria o tempo de
	// resposta revelar se o número já tinha conta.
	var users int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_user WHERE phone_e164 = $1`, testE164).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Fatal("o utilizador foi criado no request")
	}

	out, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: challenge.ChallengeID, Code: sender.code(testE164),
		DeviceID: "device-1", Platform: "ios",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsNewUser || out.UserID == "" || out.RefreshToken == "" {
		t.Fatalf("%+v", out)
	}

	// Segunda vez: a mesma conta, e já não é nova.
	again := requestOTP(t, svc, "+258 84 777 1234") // escrito de outra forma
	second, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: again.ChallengeID, Code: sender.code(testE164), DeviceID: "device-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.IsNewUser {
		t.Fatal("o mesmo número escrito de outra forma criou outra conta")
	}
	if second.UserID != out.UserID {
		t.Fatal("ids diferentes para o mesmo número")
	}
	t.Logf("conta criada no verify; o mesmo número escrito de duas formas dá a mesma conta")
}

// ⚠️ **A resposta e o tempo têm de ser iguais para número novo e existente.**
//
// Se o registo demorasse sistematicamente mais, o tempo de resposta revelava a
// informação que a resposta esconde. É por isso que o utilizador é criado no
// verify.
func TestRequestLooksAndTakesTheSameForNewAndExistingNumbers(t *testing.T) {
	svc, sender, _, _ := authSetup(t)
	ctx := context.Background()

	// Regista um número.
	known := requestOTP(t, svc, "+258849990001")
	if _, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: known.ChallengeID, Code: sender.code("+258849990001"), DeviceID: "d",
	}); err != nil {
		t.Fatal(err)
	}

	measure := func(phone string, n int) time.Duration {
		var samples []time.Duration
		for i := 0; i < n; i++ {
			start := time.Now()
			if _, err := svc.RequestOTP(ctx, service.RequestOTPInput{
				RawPhone: phone, Channel: "auto", DeviceID: "d", IPHash: "h",
			}); err != nil {
				// Os limites travam ao fim de algumas: o que interessa são as
				// que passam.
				continue
			}
			samples = append(samples, time.Since(start))
		}
		if len(samples) == 0 {
			t.Fatalf("nenhuma amostra para %s", phone)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[len(samples)/2] // mediana, imune a um pico
	}

	existing := measure("+258849990001", 4)
	fresh := measure("+258849990002", 4)

	// A resposta tem a mesma forma.
	out, err := svc.RequestOTP(ctx, service.RequestOTPInput{
		RawPhone: "+258849990003", Channel: "auto", DeviceID: "d", IPHash: "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ChallengeID == "" || out.ResendAfter == 0 {
		t.Fatal("a resposta a um número desconhecido tem de ser completa")
	}

	// E o tempo é da mesma ordem. Não se exige igualdade — há ruído de máquina —
	// mas uma diferença sistemática de ordem de grandeza seria o canal lateral.
	ratio := float64(existing) / float64(fresh)
	if ratio > 3 || ratio < 0.34 {
		t.Fatalf("número existente %v, novo %v — a diferença revela quem tem conta", existing, fresh)
	}
	t.Logf("existente %v · novo %v · rácio %.2f", existing.Round(time.Microsecond), fresh.Round(time.Microsecond), ratio)
}

// Pedir um novo invalida o anterior: dois códigos válidos ao mesmo tempo
// duplicam as tentativas.
func TestNewChallengeSupersedesTheOld(t *testing.T) {
	svc, sender, _, _ := authSetup(t)
	ctx := context.Background()

	first := requestOTP(t, svc, testPhone)
	firstCode := sender.code(testE164)
	second := requestOTP(t, svc, testPhone)

	if _, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: first.ChallengeID, Code: firstCode, DeviceID: "d",
	}); !errors.Is(err, service.ErrOTPInvalid) {
		t.Fatalf("o código antigo devia ser recusado, deu %v", err)
	}
	if _, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: second.ChallengeID, Code: sender.code(testE164), DeviceID: "d",
	}); err != nil {
		t.Fatalf("o novo devia passar: %v", err)
	}
}

// Cinco tentativas, depois o desafio morre. Reduz 10⁶ possibilidades a cinco.
func TestAttemptsAreExhausted(t *testing.T) {
	svc, sender, _, _ := authSetup(t)
	ctx := context.Background()
	challenge := requestOTP(t, svc, testPhone)

	for i := 1; i <= 4; i++ {
		_, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
			ChallengeID: challenge.ChallengeID, Code: "000000", DeviceID: "d",
		})
		if !errors.Is(err, service.ErrOTPInvalid) {
			t.Fatalf("tentativa %d: %v", i, err)
		}
		// ⚠️ A mensagem não diz quantas faltam.
		if strings.Contains(err.Error(), "4") || strings.Contains(err.Error(), "restam") {
			t.Fatalf("a mensagem conta tentativas: %v", err)
		}
	}
	if _, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: challenge.ChallengeID, Code: "000000", DeviceID: "d",
	}); !errors.Is(err, service.ErrOTPExhausted) {
		t.Fatalf("a quinta devia esgotar: %v", err)
	}
	// E depois nem o código certo entra.
	if _, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: challenge.ChallengeID, Code: sender.code(testE164), DeviceID: "d",
	}); err == nil {
		t.Fatal("o desafio estava morto e o código certo entrou")
	}
}

// Cinco minutos, e depois expira.
func TestCodeExpires(t *testing.T) {
	svc, sender, _, clk := authSetup(t)
	challenge := requestOTP(t, svc, testPhone)

	clk.Advance(6 * time.Minute)
	_, err := svc.VerifyOTP(context.Background(), service.VerifyOTPInput{
		ChallengeID: challenge.ChallengeID, Code: sender.code(testE164), DeviceID: "d",
	})
	if !errors.Is(err, service.ErrOTPExpired) {
		t.Fatalf("%v", err)
	}
}

// Os limites travam o pedido em massa.
func TestRequestIsRateLimited(t *testing.T) {
	svc, _, _, _ := authSetup(t)
	ctx := context.Background()

	allowed := 0
	for i := 0; i < 10; i++ {
		_, err := svc.RequestOTP(ctx, service.RequestOTPInput{
			RawPhone: testPhone, Channel: "auto", DeviceID: "d", IPHash: "h",
		})
		if err == nil {
			allowed++
			continue
		}
		if !errors.Is(err, service.ErrRateLimited) {
			t.Fatalf("pedido %d: %v", i, err)
		}
	}
	if allowed != 5 {
		t.Fatalf("%d pedidos passaram, o limite é 5/hora", allowed)
	}
	t.Logf("5 passaram, os restantes travados")
}

// **T3.10 — rotação com detecção de reutilização.**
func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	svc, sender, pool, _ := authSetup(t)
	ctx := context.Background()

	challenge := requestOTP(t, svc, testPhone)
	login, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: challenge.ChallengeID, Code: sender.code(testE164), DeviceID: "device-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	t1 := login.RefreshToken
	r2, err := svc.Refresh(ctx, t1, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if r2.RefreshToken == t1 {
		t.Fatal("a rotação devolveu o mesmo token")
	}

	r3, err := svc.Refresh(ctx, r2.RefreshToken, "device-1")
	if err != nil {
		t.Fatal(err)
	}

	// t1 volta a aparecer: **foi roubado**. Cai a família inteira.
	if _, err := svc.Refresh(ctx, t1, "device-1"); !errors.Is(err, repo.ErrTokenReuse) {
		t.Fatalf("esperava ErrTokenReuse, deu %v", err)
	}

	// E o token bom deixa de servir: não se sabe qual das partes é a legítima.
	if _, err := svc.Refresh(ctx, r3.RefreshToken, "device-1"); err == nil {
		t.Fatal("a família devia ter caído toda")
	}

	var live int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_token WHERE user_id = $1 AND revoked_at IS NULL`,
		login.UserID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("%d tokens vivos depois da reutilização", live)
	}
	t.Log("reutilização detectada: sessão terminada em todos os aparelhos")
}

// A auditoria regista tudo — e **sem o código**.
func TestAuthEventsAreRecordedWithoutTheCode(t *testing.T) {
	svc, sender, pool, _ := authSetup(t)
	ctx := context.Background()

	challenge := requestOTP(t, svc, testPhone)
	code := sender.code(testE164)
	_, _ = svc.VerifyOTP(ctx, service.VerifyOTPInput{ChallengeID: challenge.ChallengeID, Code: "000000", DeviceID: "d"})
	if _, err := svc.VerifyOTP(ctx, service.VerifyOTPInput{
		ChallengeID: challenge.ChallengeID, Code: code, DeviceID: "d",
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT kind, meta::text FROM auth_event ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var kinds []string
	for rows.Next() {
		var kind, meta string
		if err := rows.Scan(&kind, &meta); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
		if strings.Contains(meta, code) {
			t.Fatalf("o evento %q leva o código: %s", kind, meta)
		}
	}
	want := map[string]bool{"otp_requested": false, "otp_failed": false, "login": false}
	for _, k := range kinds {
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("faltou o evento %q (houve: %v)", k, kinds)
		}
	}
	t.Logf("auditoria: %v, sem o código", kinds)
}

// Uma entrega falhada não deixa a pessoa em silêncio.
func TestDeliveryFailureIsReported(t *testing.T) {
	svc, sender, _, _ := authSetup(t)
	sender.fail = true
	_, err := svc.RequestOTP(context.Background(), service.RequestOTPInput{
		RawPhone: testPhone, Channel: "auto", DeviceID: "d", IPHash: "h",
	})
	if !errors.Is(err, service.ErrDeliveryFailed) {
		t.Fatalf("%v", err)
	}
}
