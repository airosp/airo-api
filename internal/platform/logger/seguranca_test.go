package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// T6.10 — o código nunca pode aparecer nos registos.
//
// A defesa está no `ReplaceAttr` e não em cada chamada porque basta um
// esquecimento: quem escreve um `log.Info("enviado", "code", codigo)` às três
// da manhã não se lembra da regra, e o registo fica para sempre.
//
// Este teste existe porque a regra é invisível — nada no sítio da chamada
// mostra que o campo vai ser ocultado.
func TestOCodigoNuncaEntraNosRegistos(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: scrub}))

	const segredo = "482913"
	// Todas as maneiras plausíveis de alguém lhe chamar alguma coisa.
	log.Info("envio",
		"code", segredo,
		"otp", segredo,
		"otp_code", segredo,
		"code_hash", "abcdef",
		"CODE", segredo,
		"Otp_Code", segredo,
	)

	saida := buf.String()
	if strings.Contains(saida, segredo) {
		t.Errorf("o código apareceu no registo:\n%s", saida)
	}
	if !strings.Contains(saida, "[oculto]") {
		t.Errorf("esperava campos ocultados:\n%s", saida)
	}
}

// Os segredos de configuração também não.
func TestSegredosNaoEntramNosRegistos(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: scrub}))

	valores := map[string]string{
		"token":         "ghp_exemplo123",
		"access_token":  "eyJhbGciOi.exemplo",
		"refresh_token": "rt_exemplo",
		"authorization": "Bearer exemplo",
		"pepper":        "pepper_exemplo",
		"secret":        "segredo_exemplo",
		"password":      "palavra_exemplo",
	}
	for k, v := range valores {
		buf.Reset()
		log.Info("config", k, v)
		if strings.Contains(buf.String(), v) {
			t.Errorf("%q apareceu em claro: %s", k, buf.String())
		}
	}
}

// O número vai encurtado: país e dois últimos dígitos.
//
// Suficiente para investigar um incidente, insuficiente para identificar
// alguém — e é essa a linha que separa um registo útil de um registo que é ele
// próprio uma fuga.
func TestONumeroVaiEncurtado(t *testing.T) {
	casos := []struct{ dado, quer string }{
		{"+258841234567", "+258•••••••67"},
		{"+351912345678", "+351•••••••78"},
		{"+2588", "[oculto]"},
		{"", "[oculto]"},
	}
	for _, c := range casos {
		if got := Phone(c.dado); got != c.quer {
			t.Errorf("Phone(%q) = %q, esperava %q", c.dado, got, c.quer)
		}
	}
	// O que importa é o que **não** lá está: os dígitos do meio.
	if got := Phone("+258841234567"); strings.Contains(got, "1234") {
		t.Errorf("o meio do número ficou visível: %q", got)
	}
}

// Um registo estruturado tem de continuar a ser JSON válido depois de ocultar.
// Substituir um valor por uma cadeia parece inofensivo até o campo ser um
// número e o consumidor do registo rebentar.
func TestOcultarNaoPartaOJSON(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: scrub}))
	log.Info("pedido", "code", 482913, "status", 200)

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("o registo deixou de ser JSON: %v\n%s", err, buf.String())
	}
	if m["code"] != "[oculto]" {
		t.Errorf("code = %v", m["code"])
	}
	if m["status"] != float64(200) {
		t.Errorf("o que não é segredo tem de passar intacto: status = %v", m["status"])
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
