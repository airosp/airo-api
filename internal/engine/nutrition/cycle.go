package nutrition

import (
	"fmt"
	"math"
	"time"

	"github.com/airosp/airo-api/internal/engine/portable"
)

/*
 * O ciclo: observar, avaliar, propor, aplicar.
 *
 * ⚠️ Isto **só existia em TypeScript**. `BuildStrategy` e `BuildDayPlan` tinham
 * gémeos em Go com paridade provada; a família do ciclo não tinha nenhum, e por
 * isso a única máquina no sistema capaz de dizer "o teu alvo está errado, vamos
 * corrigi-lo" era o telemóvel de quem estivesse a olhar para o ecrã. Duas
 * consequências:
 *
 *  1. Quem trocasse de telemóvel perdia a avaliação — ela não viajava, porque
 *     não havia do outro lado quem a soubesse fazer.
 *  2. O servidor não podia propor nada sozinho. Uma adaptação que precisa de
 *     quatro semanas de registos depende de alguém abrir a app no dia certo.
 *
 * A sequência é a do §21, e a ordem importa: observar → verificar a qualidade
 * dos dados → avaliar → **esperar se a evidência for fraca** → ajustar pouco →
 * observar outra vez. Nunca "o peso subiu, corta 500 kcal": o peso é ruidoso e
 * os cortes bruscos partem a adesão, que é o que sustenta o resultado.
 */

// ── O que sai daqui ──────────────────────────────────────────────────────────

type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

/*
 * Como o corpo respondeu ao que o défice previa.
 *
 * `unknown` não é uma falha: é a resposta honesta enquanto não houver peso para
 * comparar. Fingir `as_expected` seria dizer que está tudo bem sem saber.
 */
type Response string

const (
	ResponseUnknown       Response = "unknown"
	ResponseAsExpected    Response = "as_expected"
	ResponseBelowExpected Response = "below_expected"
	ResponseAboveExpected Response = "above_expected"
)

type Signal struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type AssessmentResult struct {
	ID           string          `json:"id"`
	CreatedAtISO string          `json:"createdAtISO"`
	Adherence    AdherenceResult `json:"adherence"`
	/** O gasto que o corpo revelou. Nulo quando não há base para o dizer. */
	ObservedTDEE *int       `json:"observedTdee"`
	Response     Response   `json:"response"`
	Headline     string     `json:"headline"`
	Signals      []Signal   `json:"signals"`
	Confidence   Confidence `json:"confidence"`
}

type AdaptationResult struct {
	ID           string `json:"id"`
	CreatedAtISO string `json:"createdAtISO"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason"`
	/** Variação proposta ao alvo calórico. Nunca aplicada sem aceitação. */
	CalorieDelta int                `json:"calorieDelta"`
	MacroChanges map[string]float64 `json:"macroChanges"`
	Applied      bool               `json:"applied"`
}

type Cycle struct {
	ID           string `json:"id"`
	JourneyID    string `json:"journeyId,omitempty"`
	Index        int    `json:"index"`
	StartDateISO string `json:"startDateISO"`
	EndDateISO   string `json:"endDateISO"`
	StrategyID   string `json:"strategyId"`
	Status       string `json:"status"`
}

// ── Começar ──────────────────────────────────────────────────────────────────

type StartCycleInput struct {
	StrategyID   string
	Index        int
	StartDateISO string
	JourneyID    string
}

/*
 * StartCycle abre um ciclo de quatro semanas.
 *
 * Quatro e não duas: o peso oscila com a água, com o sal e com a hora do dia, e
 * duas semanas não chegam para separar uma tendência de um dia mau. Menos do que
 * isso é decidir sobre ruído.
 */
func StartCycle(c Config, in StartCycleInput) (Cycle, error) {
	inicio, err := time.Parse(time.RFC3339, normalizarISO(in.StartDateISO))
	if err != nil {
		return Cycle{}, fmt.Errorf("data de início inválida (%q): %w", in.StartDateISO, err)
	}
	fim := inicio.Add(time.Duration(c.CycleWeeks) * 7 * 24 * time.Hour)
	return Cycle{
		ID:           fmt.Sprintf("cycle_%d_%s", in.Index, in.StartDateISO),
		JourneyID:    in.JourneyID,
		Index:        in.Index,
		StartDateISO: in.StartDateISO,
		// O mesmo formato que o `toISOString()` do TypeScript escreve: milésimos
		// e `Z`. A paridade compara textos, e um texto diferente é um contrato
		// diferente mesmo quando o instante é o mesmo.
		EndDateISO: fim.UTC().Format("2006-01-02T15:04:05.000Z"),
		StrategyID: in.StrategyID,
		Status:     "active",
	}, nil
}

// ── Avaliar ──────────────────────────────────────────────────────────────────

type AssessCycleInput struct {
	Strategy       Strategy2
	Logs           []Log
	PeriodStartISO string
	NowISO         string
	/** Nulo quando não há duas pesagens para comparar. */
	WeightChangeKg *float64
}

/*
 * AssessCycle cruza o que foi comido com o que o corpo fez.
 *
 * É aqui que se separa "não está a cumprir" de "está a cumprir e mesmo assim
 * não responde" — duas situações com respostas opostas. Tratá-las como uma só
 * é o erro que faz uma app cortar calorias a quem já está a cumprir.
 */
func AssessCycle(c Config, in AssessCycleInput) (AssessmentResult, AdaptationResult) {
	adesao := ComputeAdherence(in.Logs, Strategy{
		CalorieTarget: in.Strategy.CalorieTarget,
		Macros:        in.Strategy.Macros,
		MealsPerDay:   in.Strategy.MealsPerDay,
	}, in.PeriodStartISO, in.NowISO)

	dias := diasEntre(in.PeriodStartISO, in.NowISO)
	avaliacao := assessNutrition(c, in.Strategy, adesao, in.WeightChangeKg, dias, in.NowISO)
	return avaliacao, adaptNutrition(c, in.Strategy, avaliacao, in.NowISO)
}

/** Pelo menos um: zero dias daria uma divisão por zero disfarçada de zero. */
func diasEntre(deISO, ateISO string) int {
	de, erroDe := time.Parse(time.RFC3339, normalizarISO(deISO))
	ate, erroAte := time.Parse(time.RFC3339, normalizarISO(ateISO))
	if erroDe != nil || erroAte != nil {
		return 1
	}
	dias := int(portable.RoundJS(ate.Sub(de).Hours() / 24))
	if dias < 1 {
		return 1
	}
	return dias
}

func assessNutrition(
	c Config, s Strategy2, adesao AdherenceResult, pesoKg *float64, dias int, agoraISO string,
) AssessmentResult {
	cfg := c.Assessment
	sinais := []Signal{}

	var observado *int
	if adesao.AverageIntakeKcal != nil && pesoKg != nil {
		observado = ObservedTDEE(c, float64(*adesao.AverageIntakeKcal), *pesoKg, dias)
	}

	/*
	 * Confiança antes de conclusão.
	 *
	 * Um número tirado de três dias de registo é pior do que nenhum: parece
	 * informação. Por isso a falta de dados tem uma saída própria e não um
	 * palpite mais fraco.
	 */
	dadosBastantes := adesao.Evaluable && adesao.LoggedDays >= cfg.MinLoggedDays
	confianca := ConfidenceMedium
	switch {
	case !dadosBastantes:
		confianca = ConfidenceLow
	case adesao.LoggedDays >= 20:
		confianca = ConfidenceHigh
	}

	if !dadosBastantes {
		titulo := "Ainda não há refeições registadas."
		if adesao.Evaluable {
			titulo = fmt.Sprintf("Ainda são poucos dias registados (%d de %d).",
				adesao.LoggedDays, cfg.MinLoggedDays)
		}
		return AssessmentResult{
			ID: "nutrition-assessment-" + agoraISO, CreatedAtISO: agoraISO,
			Adherence: adesao, ObservedTDEE: observado,
			Response: ResponseUnknown, Headline: titulo,
			Signals: sinais, Confidence: confianca,
		}
	}

	// Desvios de ingestão.
	if adesao.AverageIntakeKcal != nil && s.CalorieTarget > 0 {
		desvio := (float64(*adesao.AverageIntakeKcal) - float64(s.CalorieTarget)) / float64(s.CalorieTarget)
		switch {
		case desvio > cfg.CalorieTolerance:
			sinais = append(sinais, Signal{"intake_above", "warning",
				fmt.Sprintf("A ingestão média está %d%% acima do alvo.", int(portable.RoundJS(desvio*100)))})
		case desvio < -cfg.CalorieTolerance:
			sinais = append(sinais, Signal{"intake_below", "warning",
				fmt.Sprintf("A ingestão média está %d%% abaixo do alvo.", int(portable.RoundJS(math.Abs(desvio)*100)))})
		}
	}
	if adesao.Protein < 1-cfg.ProteinTolerance {
		sinais = append(sinais, Signal{"protein_low", "warning",
			fmt.Sprintf("A proteína está em %d%% do alvo.", int(portable.RoundJS(adesao.Protein*100)))})
	}

	/*
	 * A resposta do corpo face ao que o défice previa.
	 *
	 * Acima de 1,5× o previsto também é desvio — só que para o outro lado, e
	 * perder depressa de mais custa músculo. Abaixo de 0,2 kg previstos não se
	 * conclui nada: é menos do que a balança distingue de um dia para o outro.
	 */
	previsto := (float64(s.EnergyAdjustment) * float64(dias)) / c.KcalPerKg
	resposta := ResponseUnknown
	switch {
	case pesoKg != nil && math.Abs(previsto) > 0.2:
		conseguido := *pesoKg / previsto
		switch {
		case conseguido > 1.5:
			resposta = ResponseAboveExpected
		case conseguido >= 0.75:
			resposta = ResponseAsExpected
		default:
			resposta = ResponseBelowExpected
		}
	case pesoKg != nil:
		resposta = ResponseAsExpected
	}

	if observado != nil && s.BasisTDEE > 0 {
		fenda := (float64(*observado) - float64(s.BasisTDEE)) / float64(s.BasisTDEE)
		if math.Abs(fenda) >= cfg.RecalibrationThreshold {
			lado := "abaixo"
			if fenda > 0 {
				lado = "acima"
			}
			sinais = append(sinais, Signal{"tdee_drift", "info",
				fmt.Sprintf("O gasto observado (%d kcal) está %s do estimado (%d kcal).",
					*observado, lado, s.BasisTDEE)})
		}
	}

	return AssessmentResult{
		ID: "nutrition-assessment-" + agoraISO, CreatedAtISO: agoraISO,
		Adherence: adesao, ObservedTDEE: observado,
		Response: resposta, Headline: manchete(c, adesao, resposta, sinais),
		Signals: sinais, Confidence: confianca,
	}
}

func manchete(c Config, adesao AdherenceResult, resposta Response, sinais []Signal) string {
	forte := adesao.Score >= c.Assessment.StrongAdherence
	if forte && resposta == ResponseAsExpected {
		return "A alimentação está a acompanhar o plano."
	}
	if forte && resposta == ResponseBelowExpected {
		return "Estás a cumprir, mas o corpo responde mais devagar do que o previsto."
	}
	if adesao.Score < c.Assessment.WeakAdherence {
		return "O plano alimentar está a ser difícil de seguir."
	}
	for _, s := range sinais {
		if s.Severity != "info" {
			return "Há um ajuste a fazer na alimentação."
		}
	}
	return "A alimentação está no caminho."
}

// ── Propor ───────────────────────────────────────────────────────────────────

func adaptNutrition(c Config, s Strategy2, a AssessmentResult, agoraISO string) AdaptationResult {
	base := AdaptationResult{
		ID: "nutrition-adaptation-" + agoraISO, CreatedAtISO: agoraISO,
		MacroChanges: map[string]float64{},
	}
	cfg := c.Assessment

	/*
	 * O passo é pequeno **e** proporcional.
	 *
	 * 150 kcal num alvo de 3000 é um ajuste; 150 kcal num alvo de 1400 é um
	 * corte. O tecto de 10% impede o segundo sem tornar o primeiro inútil.
	 */
	passo := c.Adaptation.StepKcal
	if proporcional := int(portable.RoundJS(float64(s.CalorieTarget) * c.Adaptation.MaxStepRatio)); proporcional < passo {
		passo = proporcional
	}

	if a.Confidence == ConfidenceLow {
		base.Kind = "await_data"
		base.Reason = "Ainda não há dias suficientes registados para decidir com confiança."
		base.Applied = true
		return base
	}

	// Quem não consegue seguir o plano não precisa de um plano mais apertado.
	if a.Adherence.Score < cfg.WeakAdherence {
		base.Kind = "hold"
		base.Reason = "A prioridade agora é tornar o plano seguível, não apertá-lo."
		return base
	}

	if a.Adherence.Protein < 0.8 {
		base.Kind = "raise_protein"
		base.Reason = "A proteína está consistentemente abaixo do alvo — é o primeiro ajuste a fazer."
		base.MacroChanges = map[string]float64{"protein": float64(s.Macros.Protein)}
		return base
	}

	// Cumpre tudo e o corpo não responde: o alvo é que está errado, não a pessoa.
	if a.Adherence.Score >= cfg.StrongAdherence && a.Response == ResponseBelowExpected {
		if a.ObservedTDEE != nil {
			base.Kind = "recalibrate_energy"
			base.Reason = fmt.Sprintf(
				"O gasto real parece ser %d kcal, e não %d. Vale a pena recalibrar o alvo a partir daí.",
				*a.ObservedTDEE, s.BasisTDEE)
		} else {
			base.Kind = "increase_deficit"
			base.Reason = "Estás a cumprir o plano: um ajuste pequeno no alvo deve destravar o progresso."
		}
		base.CalorieDelta = -passo
		return base
	}

	if a.Response == ResponseAboveExpected && s.GoalType == LoseFat {
		base.Kind = "reduce_deficit"
		base.Reason = "A perda está a acontecer mais depressa do que o previsto. Aliviar o défice protege a massa muscular."
		base.CalorieDelta = passo
		return base
	}

	for _, sinal := range a.Signals {
		if sinal.ID == "intake_above" {
			base.Kind = "hold"
			base.Reason = "A ingestão média está acima do alvo. O alvo mantém-se; o que precisa de atenção é o cumprimento."
			return base
		}
	}

	base.Kind = "hold"
	base.Reason = "A estratégia continua adequada ao que os dados mostram."
	base.Applied = true
	return base
}

// ── Aplicar ──────────────────────────────────────────────────────────────────

type ApplyAdaptationInput struct {
	Strategy     Strategy2
	Adaptation   AdaptationResult
	BodyWeightKg float64
	NowISO       string
}

/*
 * ApplyAdaptation produz a estratégia seguinte a partir de uma proposta aceite.
 *
 * O piso calórico não é uma sugestão: uma proposta que o atravesse é travada
 * aqui, e não no ecrã. E a estratégia que sai daqui é logo verificada — se
 * alguma invariante falhar, é defeito do motor e não escolha de ninguém, por
 * isso sai à vista em vez de em silêncio.
 */
func ApplyAdaptation(c Config, in ApplyAdaptationInput) (Strategy2, []Signal) {
	base := in.Strategy.BasisTDEE
	if in.Adaptation.Kind == "recalibrate_energy" && in.Adaptation.CalorieDelta != 0 {
		base = in.Strategy.BasisTDEE + in.Adaptation.CalorieDelta
	}

	alvo := in.Strategy.CalorieTarget + in.Adaptation.CalorieDelta
	if alvo < c.MinDailyCalories {
		alvo = c.MinDailyCalories
	}

	proxima := in.Strategy
	proxima.CalorieTarget = alvo
	proxima.Macros = DistributeMacros(c, alvo, in.BodyWeightKg, in.Strategy.GoalType)
	proxima.BasisTDEE = base
	// Aproximou-se do gasto observado sem lá saltar: é um ajuste, não uma medição.
	if in.Adaptation.Kind == "recalibrate_energy" {
		proxima.BasisSource = "adjusted"
	}
	return proxima, CheckStrategySafety(c, proxima, in.BodyWeightKg)
}

/*
 * CheckStrategySafety verifica o que nenhuma estratégia pode violar.
 *
 * Corre **depois** de a estratégia estar montada: se alguma destas falhar é bug
 * do motor, não escolha do utilizador — e um bug que ninguém vê é um bug que
 * fica.
 */
func CheckStrategySafety(c Config, s Strategy2, bodyWeightKg float64) []Signal {
	sinais := []Signal{}

	if s.CalorieTarget < c.MinDailyCalories {
		sinais = append(sinais, Signal{"below_floor", "critical",
			"O alvo calórico ficou abaixo do piso permitido."})
	}
	if float64(s.Macros.Fat)*9 < float64(s.CalorieTarget)*c.MinFatRatio*0.95 {
		sinais = append(sinais, Signal{"fat_too_low", "warning",
			"A gordura ficou abaixo do mínimo recomendado."})
	}
	if float64(s.Macros.Carbs) < float64(c.MinCarbsG)*0.95 {
		sinais = append(sinais, Signal{"carbs_too_low", "warning",
			"Os hidratos ficaram abaixo do mínimo para sustentar o treino."})
	}
	if bodyWeightKg > 0 && float64(s.Macros.Protein)/bodyWeightKg > 3 {
		sinais = append(sinais, Signal{"protein_excessive", "warning",
			"A proteína ficou acima do que é habitualmente recomendado."})
	}
	// 3% de folga cobre o arredondamento das gramas; acima disso há erro de cálculo.
	macros := float64(s.Macros.Protein)*4 + float64(s.Macros.Carbs)*4 + float64(s.Macros.Fat)*9
	if math.Abs(macros-float64(s.CalorieTarget)) > float64(s.CalorieTarget)*0.03 {
		sinais = append(sinais, Signal{"macro_mismatch", "critical",
			"Os macros não somam o alvo calórico."})
	}
	return sinais
}

/*
 * normalizarISO aceita um dia solto onde o TypeScript aceita.
 *
 * `new Date("2026-09-16")` é meia-noite UTC em JavaScript; `time.Parse` com
 * RFC3339 recusa a mesma cadeia. Sem isto, a paridade partia-se no formato e
 * não na regra.
 */
func normalizarISO(s string) string {
	if len(s) == 10 {
		return s + "T00:00:00Z"
	}
	return s
}
