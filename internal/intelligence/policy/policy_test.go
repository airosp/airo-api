package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/intelligence/contracts"
)

func TestRedactTextTiraOQueIdentifica(t *testing.T) {
	in := "Comi xima com matapa\n— liga-me +258 84 123 4567 ou ana@exemplo.co.mz, https://x.y/z"
	got := RedactText(in)
	for _, proibido := range []string{"84 123", "ana@", "https://", "\n"} {
		if strings.Contains(got, proibido) {
			t.Errorf("ficou %q em %q", proibido, got)
		}
	}
	if !strings.Contains(got, "Comi xima com matapa") {
		t.Errorf("tirou a refeição: %q", got)
	}
}

// As quantidades não são números de telefone.
func TestRedactTextDeixaAsQuantidades(t *testing.T) {
	in := "2 ovos, 250 g de arroz e 1,5 l de água"
	if got := RedactText(in); got != in {
		t.Errorf("mudou %q para %q", in, got)
	}
}

func TestRedactTextCortaOComprimento(t *testing.T) {
	got := RedactText(strings.Repeat("arroz ", 200))
	if n := len([]rune(got)); n > MaxMealText {
		t.Errorf("%d caracteres", n)
	}
}

func TestCustoArredondaParaCima(t *testing.T) {
	p := Prices{InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10}
	// 1 token de entrada custa 1,25 milionésimos: tem de contar 2.
	if got := p.CostMicroUSD(1, 0); got != 2 {
		t.Errorf("custo %d", got)
	}
	if got := p.CostMicroUSD(1_000_000, 100_000); got != 2_250_000 {
		t.Errorf("custo %d", got)
	}
}

func TestOrcamento(t *testing.T) {
	b := Budget{MonthlyMicroUSD: 10_000_000, AlertRatio: 0.8}
	if b.Exhausted(9_999_999) || !b.Exhausted(10_000_000) {
		t.Error("tecto mal calculado")
	}
	if b.NearLimit(7_999_999) || !b.NearLimit(8_000_000) {
		t.Error("aviso mal calculado")
	}
	if (Budget{}).Exhausted(1 << 40) {
		t.Error("sem tecto não esgota — e a configuração recusa arrancar sem ele")
	}
}

func TestInicioDoMesEDoDia(t *testing.T) {
	now := time.Date(2026, 10, 22, 23, 30, 0, 0, time.FixedZone("CAT", 2*3600))
	if got := MonthStart(now); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("mês %v", got)
	}
	if got := DayStart(now); !got.Equal(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("dia %v", got)
	}
}

func TestInterruptores(t *testing.T) {
	f := Features{Enabled: true, MealPhoto: true}
	if !f.Allows(contracts.TaskMealPhoto) || f.Allows(contracts.TaskMealText) {
		t.Error("cada funcionalidade liga-se sozinha")
	}
	f.Enabled = false
	if f.Allows(contracts.TaskMealPhoto) {
		t.Error("o interruptor geral desliga tudo")
	}
}

func TestConsentimentoDeOutraVersaoNaoVale(t *testing.T) {
	if !ConsentValid(PurposeMealPhoto, PolicyVersions[PurposeMealPhoto]) {
		t.Error("a versão actual tem de valer")
	}
	if ConsentValid(PurposeMealPhoto, "2020-01-01") {
		t.Error("uma versão antiga não vale")
	}
	if KnownPurpose(PurposeProgress) {
		t.Error("o progresso ainda não tem texto publicado — não se pode autorizar")
	}
}
