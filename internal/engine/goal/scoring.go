package goal

import "github.com/airosp/airo-api/internal/engine/portable"

// dimensionOf mapeia a categoria da regra para a dimensão que ela penaliza.
// Segurança pesa no corpo; recuperação no treino; nutrição no ritmo.
var dimensionOf = map[Category]string{
	CatBody: "body", CatSafety: "body",
	CatRate: "rate", CatNutrition: "rate",
	CatTraining: "training", CatRecovery: "training",
}

// ScoreGoal — cada dimensão começa em 100 e é penalizada pelos sinais da sua
// categoria.
//
// ⚠️ O score é **interno**. A configuração diz: "Nunca são mostrados ao
// utilizador". Serve para escolher o tom da mensagem, não para ser desenhado.
func ScoreGoal(c Config, signals []Signal, hasHistory bool) Scores {
	p := c.Scoring.Penalty
	w := c.Scoring.Weights

	// Sem histórico não há consistência para avaliar. Parte-se de um valor
	// neutro em vez de premiar ou castigar quem ainda não tem dados.
	consistency := 70
	if hasHistory {
		consistency = 100
	}
	s := map[string]int{"body": 100, "rate": 100, "training": 100, "consistency": consistency}

	for _, sig := range signals {
		dim, ok := dimensionOf[sig.Category]
		if !ok {
			continue
		}
		penalty := p.Info
		switch sig.Severity {
		case Warning:
			penalty = p.Warning
		case Critical:
			penalty = p.Critical
		}
		if s[dim] = s[dim] - penalty; s[dim] < 0 {
			s[dim] = 0
		}
	}

	overall := int(portable.RoundJS(
		float64(s["body"])*w.Body +
			float64(s["rate"])*w.Rate +
			float64(s["training"])*w.Training +
			float64(s["consistency"])*w.Consistency))

	return Scores{
		Body: s["body"], Rate: s["rate"], Training: s["training"],
		Consistency: s["consistency"], Overall: overall,
	}
}

// StatusFrom traduz o score num estado.
//
// Um sinal **crítico** sobrepõe-se ao score: por muito bem que as outras
// dimensões pontuem, uma meta com um problema grave não pode sair como
// "realista". E um aviso de segurança impede os dois estados positivos — uma
// meta com um conflito por resolver não pode ser anunciada como boa meta.
func StatusFrom(c Config, scores Scores, signals []Signal) Status {
	for _, s := range signals {
		if s.Severity == Critical {
			return StatusNeedsReview
		}
	}

	capped := false
	for _, s := range signals {
		if s.Category == CatSafety && s.Severity == Warning {
			capped = true
			break
		}
	}

	sc := c.Scoring
	switch {
	case !capped && scores.Overall >= sc.Excellent:
		return StatusExcellent
	case !capped && scores.Overall >= sc.Realistic:
		return StatusRealistic
	case scores.Overall >= sc.Ambitious:
		return StatusAmbitious
	case scores.Overall >= sc.Aggressive:
		return StatusAggressive
	default:
		return StatusNeedsReview
	}
}
