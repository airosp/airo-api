package service

import (
	"context"
	"errors"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/platform/clock"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// MealPreferences guarda e lê as trocas de refeição de um dia.
type MealPreferences interface {
	Read(ctx context.Context, userID string, day time.Time) (map[string]int, error)
	Bump(ctx context.Context, userID string, day time.Time, slot string) (int, error)
	Reset(ctx context.Context, userID string, day time.Time, slot string) error
}

// StrategyReader lê a estratégia nutricional em vigor.
type StrategyReader interface {
	CurrentStrategy(ctx context.Context, userID string, day time.Time) (repo.StrategyRow, error)
}

/*
 * DayPlanStore guarda o plano que foi servido, e devolve-o.
 *
 * ⚠️ O dia era montado a cada pedido e nunca guardado — e um dia montado é
 * função da estratégia **de agora** e do perfil **de agora**. Perguntar por
 * ontem devolvia por isso um dia que nunca existiu: quem mudasse o objectivo
 * na quarta via a terça reescrita com as calorias de hoje, ao lado de um
 * diário que registava a terça a sério.
 */
type DayPlanStore interface {
	SaveDay(ctx context.Context, userID, strategyID string, day time.Time, plan nutrition.DayPlan) error
	Day(ctx context.Context, userID string, day time.Time) (repo.StoredDay, error)
}

type NutritionService struct {
	strategies StrategyReader
	meals      MealPreferences
	logs       LogReader
	weights    WeightReader
	plans      DayPlanStore
	clk        clock.Clock
	nutCfg     nutrition.Config
	goalCfg    goal.Config
}

func NewNutritionService(s StrategyReader, meals MealPreferences, nutCfg nutrition.Config, goalCfg goal.Config) *NutritionService {
	return &NutritionService{strategies: s, meals: meals, nutCfg: nutCfg, goalCfg: goalCfg}
}

/*
 * ComAvaliacao liga o que a avaliação do ciclo precisa de ler.
 *
 * Separado do construtor porque a maioria de quem usa este serviço só quer o
 * plano do dia: obrigar toda a gente a passar dois repositórios que não usa era
 * espalhar a avaliação por sítios que não têm nada a ver com ela.
 */
func (s *NutritionService) ComAvaliacao(logs LogReader, weights WeightReader) *NutritionService {
	s.logs, s.weights = logs, weights
	return s
}

/*
 * ComPlanoGuardado liga o sítio onde o dia servido fica escrito.
 *
 * Separado do construtor pela mesma razão que a avaliação: quem monta este
 * serviço num teste do motor não tem base de dados nenhuma, e sem armazém o
 * comportamento é o de sempre — monta-se o dia e serve-se.
 */
func (s *NutritionService) ComPlanoGuardado(plans DayPlanStore, clk clock.Clock) *NutritionService {
	s.plans, s.clk = plans, clk
	return s
}

// NutritionTodayInput é o que o motor precisa e o pedido não traz. Vem todo do
// perfil: receber a dieta no corpo era deixar o cliente escolher o que come
// hoje, e a escolha é do plano.
type NutritionTodayInput struct {
	UserID string

	Diet        nutrition.DietProfile
	Training    nutrition.TrainingLoad
	TrainsToday bool

	// O corpo, para o caso de ainda não haver estratégia gravada.
	WeightKg       float64
	HeightCm       *float64
	Age            *int
	Sex            *string
	DaysPerWeek    int
	SessionMinutes int

	LocalDay time.Time
}

type NutritionToday struct {
	Strategy nutrition.Strategy2
	Day      nutrition.DayPlan
	// FromStoredStrategy diz se o alvo veio da decisão gravada ou de uma conta
	// feita agora. Não é detalhe de implementação: é a diferença entre um
	// número que a pessoa aceitou e um número que apareceu.
	FromStoredStrategy bool
	// Swapped são as refeições que a pessoa trocou, para o ecrã as poder marcar
	// como escolha dela e não como proposta.
	Swapped map[string]bool
	// HydrationMl é o alvo de água do dia. Acompanha o peso e o treino.
	HydrationMl int
}

// aplicarTrocas remonta as refeições que a pessoa trocou.
func (s *NutritionService) aplicarTrocas(ctx context.Context, in NutritionTodayInput, day *nutrition.DayPlan) (map[string]bool, error) {
	trocadas := map[string]bool{}
	if s.meals == nil {
		return trocadas, nil
	}
	variantes, err := s.meals.Read(ctx, in.UserID, in.LocalDay)
	if err != nil {
		return nil, err
	}
	if len(variantes) == 0 {
		return trocadas, nil
	}

	for i, meal := range day.Meals {
		variante, ok := variantes[string(meal.Slot)]
		if !ok {
			continue
		}
		/*
		 * Uma remontagem por toque, em cadeia.
		 *
		 * `RebuildMeal` procura uma composição diferente da que recebe. Partir
		 * sempre da proposta original fazia o segundo toque devolver o que o
		 * primeiro já tinha dado — a pessoa carregava e nada mudava.
		 *
		 * Refazer a cadeia reproduz exactamente a sequência de toques que o
		 * telemóvel fazia quando isto vivia lá. São poucas iterações: é o número
		 * de vezes que alguém carregou hoje naquela refeição.
		 */
		nova := meal
		for v := 1; v <= variante; v++ {
			nova, err = nutrition.RebuildMeal(s.nutCfg, nutrition.RebuildMealInput{
				Meal: nova, Diet: in.Diet, Variant: v,
			})
			if err != nil {
				return nil, err
			}
		}
		day.Meals[i] = nova
		trocadas[string(meal.Slot)] = true
	}
	return trocadas, nil
}

// SwapMeal troca uma refeição por outra proposta, e devolve o dia inteiro.
//
// Devolve o dia todo e não só a refeição: o ecrã mostra o dia, e uma resposta
// parcial obrigava-o a juntar duas verdades — que é como se perde a soma.
func (s *NutritionService) SwapMeal(ctx context.Context, in NutritionTodayInput, slot string) (NutritionToday, error) {
	if s.meals == nil {
		return NutritionToday{}, ErrSemTrocas
	}
	if _, err := s.meals.Bump(ctx, in.UserID, in.LocalDay, slot); err != nil {
		return NutritionToday{}, err
	}
	return s.Today(ctx, in)
}

// ResetMeal devolve uma refeição à proposta do plano.
func (s *NutritionService) ResetMeal(ctx context.Context, in NutritionTodayInput, slot string) (NutritionToday, error) {
	if s.meals == nil {
		return NutritionToday{}, ErrSemTrocas
	}
	if err := s.meals.Reset(ctx, in.UserID, in.LocalDay, slot); err != nil {
		return NutritionToday{}, err
	}
	return s.Today(ctx, in)
}

// ErrSemTrocas — o serviço foi montado sem onde guardar as trocas.
var ErrSemTrocas = errors.New("trocas de refeição indisponíveis")

/*
 * Today monta o plano alimentar do dia — ou devolve o que foi servido, se o dia
 * já passou.
 *
 * ⚠️ **Um dia passado não se remonta.** Montar é função da estratégia e do
 * perfil de agora; aplicada a ontem, dá um plano que ninguém viu. O que a
 * pessoa comeu está no diário e não muda — o plano ao lado dele também não
 * pode mudar, senão o histórico passa a comparar o registo de ontem com a
 * intenção de hoje.
 *
 * Hoje e os dias por vir montam-se sempre: é aí que uma estratégia adaptada,
 * um perfil corrigido ou uma exclusão nova têm de aparecer. E o que se serviu
 * fica escrito, para amanhã este mesmo dia já ser passado.
 */
func (s *NutritionService) Today(ctx context.Context, in NutritionTodayInput) (NutritionToday, error) {
	strategy, stored, strategyID, err := s.estrategiaEID(ctx, in)
	if err != nil {
		return NutritionToday{}, err
	}

	if guardado, ok := s.diaGuardado(ctx, in); ok {
		trocadas, err := s.meals.Read(ctx, in.UserID, in.LocalDay)
		if err != nil {
			return NutritionToday{}, err
		}
		marcadas := map[string]bool{}
		for slot, n := range trocadas {
			if n > 0 {
				marcadas[slot] = true
			}
		}
		return NutritionToday{
			Strategy: strategy, Day: guardado, FromStoredStrategy: stored,
			Swapped: marcadas,
			HydrationMl: nutrition.HydrationTarget(s.nutCfg, nutrition.HydrationInput{
				BodyWeightKg:   in.WeightKg,
				TrainsToday:    in.TrainsToday,
				SessionMinutes: in.SessionMinutes,
			}),
		}, nil
	}

	day, err := nutrition.BuildDayPlan(s.nutCfg, nutrition.BuildDayPlanInput{
		Strategy:    strategy,
		Diet:        in.Diet,
		Training:    in.Training,
		DayISO:      in.LocalDay.Format("2006-01-02"),
		TrainsToday: in.TrainsToday,
	})
	if err != nil {
		return NutritionToday{}, err
	}

	// As trocas da pessoa entram por cima da proposta. O alvo de cada refeição
	// não muda — `RebuildMeal` preserva-o —, por isso o dia continua a fechar.
	trocadas, err := s.aplicarTrocas(ctx, in, &day)
	if err != nil {
		return NutritionToday{}, err
	}
	agua := nutrition.HydrationTarget(s.nutCfg, nutrition.HydrationInput{
		BodyWeightKg:   in.WeightKg,
		TrainsToday:    in.TrainsToday,
		SessionMinutes: in.SessionMinutes,
	})

	/*
	 * Guardar o que se serviu — e não falhar o pedido se a gravação falhar.
	 *
	 * O dia já está montado e é o correcto; recusá-lo porque a escrita não
	 * passou seria trocar um histórico imperfeito por um ecrã vazio. Só se
	 * guarda com estratégia gravada: `daily_plan.strategy_id` aponta para
	 * `nutrition_strategy`, e uma estratégia calculada na hora não tem linha
	 * nenhuma para onde apontar.
	 */
	if s.plans != nil && stored && strategyID != "" && !s.diaPassado(in.LocalDay) {
		_ = s.plans.SaveDay(ctx, in.UserID, strategyID, in.LocalDay, day)
	}

	return NutritionToday{
		Strategy: strategy, Day: day, FromStoredStrategy: stored,
		Swapped: trocadas, HydrationMl: agua,
	}, nil
}

/*
 * diaGuardado devolve o plano escrito, quando o dia pedido já passou.
 *
 * Só para o passado. Hoje monta-se sempre — é o dia que ainda pode mudar, e
 * uma troca ou uma adaptação têm de se ver sem esperar pela meia-noite.
 *
 * Sem armazém, sem relógio, ou sem nada escrito para aquele dia, devolve
 * `false` e o caminho é o de sempre: montar. É também o que acontece a todos
 * os dias anteriores a esta funcionalidade existir, que nunca foram escritos.
 */
func (s *NutritionService) diaGuardado(ctx context.Context, in NutritionTodayInput) (nutrition.DayPlan, bool) {
	if s.plans == nil || !s.diaPassado(in.LocalDay) {
		return nutrition.DayPlan{}, false
	}
	guardado, err := s.plans.Day(ctx, in.UserID, in.LocalDay)
	if err != nil {
		return nutrition.DayPlan{}, false
	}
	return guardado.Plan, true
}

/*
 * diaPassado diz se o dia pedido já acabou.
 *
 * Decide as duas metades: um dia passado lê-se do que ficou escrito, e **não**
 * se escreve. Remontar ontem para o guardar era inventar história — passaria a
 * ser para sempre "o plano de ontem" um plano que ninguém viu naquele dia.
 * Fica-se pelo que se sabe: os dias que esta funcionalidade não apanhou
 * continuam a ser montados, e a dizê-lo por não estarem guardados.
 *
 * Sem relógio não se pode decidir, e então nada é passado: é o comportamento
 * de sempre, que é o certo para quem monta este serviço sem base de dados.
 */
func (s *NutritionService) diaPassado(dia time.Time) bool {
	if s.clk == nil {
		return false
	}
	hoje := s.clk.Now().UTC().Truncate(24 * time.Hour)
	return dia.UTC().Truncate(24 * time.Hour).Before(hoje)
}

// strategyFor prefere a decisão gravada e só calcula quando não há nenhuma.
//
// Quem ainda não criou objectivo não fica sem plano — fica com um de manutenção,
// que é a leitura honesta de "ainda não disseste o que queres".
func (s *NutritionService) strategyFor(ctx context.Context, in NutritionTodayInput) (nutrition.Strategy2, bool, error) {
	e, gravada, _, err := s.estrategiaEID(ctx, in)
	return e, gravada, err
}

// estrategiaEID é o `strategyFor` com o identificador da linha, que é o que o
// plano guardado precisa: `daily_plan.strategy_id` diz qual foi a estratégia
// que produziu aquele dia, e é por ela que se sabe que o dia guardado ainda
// descreve o alvo em vigor.
func (s *NutritionService) estrategiaEID(ctx context.Context, in NutritionTodayInput) (nutrition.Strategy2, bool, string, error) {
	row, err := s.strategies.CurrentStrategy(ctx, in.UserID, in.LocalDay)
	switch {
	case err == nil:
		return nutrition.Strategy2{
			GoalType:      nutrition.GoalType(row.Goal),
			CalorieTarget: row.CalorieTarget,
			Macros: nutrition.Macros{
				Protein: row.ProteinG, Carbs: row.CarbsG, Fat: row.FatG,
			},
			MealsPerDay:      in.Diet.MealsPerDay,
			EnergyAdjustment: row.CalorieTarget - row.TDEEEstimated,
			BasisTDEE:        row.TDEEEstimated,
			BasisSource:      "estimated",
		}, true, row.ID, nil
	case !errors.Is(err, repo.ErrNotFound):
		return nutrition.Strategy2{}, false, "", err
	}

	// Sem estratégia gravada: o gasto sai do motor de objectivos, que é quem
	// sabe de corpos. Sem altura nem idade não há fórmula, e o `BuildStrategy`
	// cai no recurso — dizê-lo é melhor do que inventar precisão.
	var tdee *float64
	bmr := goal.BasalMetabolicRate(goal.Body{
		CurrentWeightKg: in.WeightKg, HeightCm: in.HeightCm, Age: in.Age,
		Sex: sexoDe(in.Sex),
	})
	factor := goal.ActivityFactorFor(s.goalCfg, goal.WeeklyTrainingMinutes(in.DaysPerWeek, in.SessionMinutes))
	if total := goal.TotalDailyEnergy(bmr, factor); total != nil {
		v := float64(total.Estimate)
		tdee = &v
	}

	return nutrition.BuildStrategy(s.nutCfg, nutrition.BuildStrategyInput{
		Energy:       nutrition.EnergyProfile{TDEEKcal: tdee},
		GoalType:     nutrition.Maintain,
		BodyWeightKg: in.WeightKg,
		Diet:         in.Diet,
	}), false, "", nil
}

func sexoDe(s *string) *goal.Sex {
	if s == nil {
		return nil
	}
	v := goal.Sex(*s)
	return &v
}

// ── Reequilíbrio ─────────────────────────────────────────────────────────────

// RebalanceResult é o que o ecrã mostra antes de alguém aceitar.
type RebalanceResult struct {
	Day      nutrition.DayPlan
	Consumed int
	Delta    int
	Applied  bool
	Floored  bool
}

// Rebalance propõe o resto do dia depois de uma refeição ter corrido diferente.
//
// ⚠️ **Só a pedido, e só propõe.** Reequilibrar sozinho a cada registo
// transforma um almoço pesado num jantar de 300 kcal sem ninguém pedir, e a
// pessoa descobre pelo prato.
//
// `consumed` vem do cliente porque é ele que tem o diário do dia a decorrer —
// o servidor tem os registos que já subiram, e a refeição que acabou de ser
// comida pode ainda não ter chegado.
func (s *NutritionService) Rebalance(ctx context.Context, in NutritionTodayInput, consumed int, comidas map[string]bool) (RebalanceResult, error) {
	hoje, err := s.Today(ctx, in)
	if err != nil {
		return RebalanceResult{}, err
	}

	// As que faltam: as que ainda não foram comidas, pela ordem do dia.
	faltam := make([]nutrition.PlannedMeal, 0, len(hoje.Day.Meals))
	for _, m := range hoje.Day.Meals {
		if !comidas[string(m.Slot)] {
			faltam = append(faltam, m)
		}
	}

	out := nutrition.Rebalance(nutrition.RebalanceInput{
		DayTarget: hoje.Day.Kcal, Consumed: consumed, Remaining: faltam,
	})

	// Recompõe o dia: as comidas ficam como estavam — já foram — e as que
	// faltam levam o alvo novo.
	novo := hoje.Day
	novo.Meals = make([]nutrition.PlannedMeal, 0, len(hoje.Day.Meals))
	i := 0
	for _, m := range hoje.Day.Meals {
		if comidas[string(m.Slot)] {
			novo.Meals = append(novo.Meals, m)
			continue
		}
		novo.Meals = append(novo.Meals, out.Meals[i])
		i++
	}

	return RebalanceResult{
		Day: novo, Consumed: consumed,
		Delta: out.Delta, Applied: out.Applied, Floored: out.Floored,
	}, nil
}

// ── A avaliação do ciclo ─────────────────────────────────────────────────────

// LogReader lê os registos de refeição de um intervalo.
type LogReader interface {
	Logs(ctx context.Context, userID string, from, to time.Time) ([]repo.MealLog, error)
}

// WeightReader lê a série do peso, para saber o que o corpo fez no período.
type WeightReader interface {
	Series(ctx context.Context, userID, metric string, since time.Time) ([]repo.MeasurementEntry, error)
}

type CycleAssessment struct {
	Cycle      nutrition.Cycle
	Assessment nutrition.AssessmentResult
	Adaptation nutrition.AdaptationResult
	Strategy   nutrition.Strategy2
}

/*
 * AssessCycle avalia o ciclo em curso e propõe o passo seguinte.
 *
 * ⚠️ Isto **só existia no telemóvel**. A única máquina capaz de dizer "o teu
 * alvo está errado, vamos corrigi-lo" era o aparelho de quem estivesse a olhar
 * para o ecrã: quem trocasse de telemóvel perdia a avaliação, e o servidor não
 * podia propor nada sozinho.
 *
 * **O ciclo é o período em que a estratégia esteve de pé** — `effective_from`.
 * Há uma tabela `nutrition_cycle` no esquema desde o primeiro dia e nunca
 * ninguém lhe escreveu uma linha; guardar ali uma segunda data era arranjar
 * duas verdades sobre quando isto começou.
 */
func (s *NutritionService) AssessCycle(ctx context.Context, in NutritionTodayInput) (CycleAssessment, error) {
	if s.logs == nil || s.weights == nil {
		return CycleAssessment{}, errors.New("avaliação do ciclo indisponível")
	}
	linha, err := s.strategies.CurrentStrategy(ctx, in.UserID, in.LocalDay)
	if errors.Is(err, repo.ErrNotFound) {
		return CycleAssessment{}, ErrSemEstrategia
	}
	if err != nil {
		return CycleAssessment{}, err
	}

	inicio := linha.EffectiveFrom.UTC()
	agora := in.LocalDay.UTC()
	estrategia := estrategiaDaLinha(linha)

	registos, err := s.logs.Logs(ctx, in.UserID, inicio, agora)
	if err != nil {
		return CycleAssessment{}, err
	}

	// `body_weight` e não `weight`: é o nome no enum `metric_key`, e o Postgres
	// recusa o outro em vez de o ignorar — que é a forma certa de recusar.
	pesos, err := s.weights.Series(ctx, in.UserID, "body_weight", inicio)
	if err != nil {
		return CycleAssessment{}, err
	}

	ciclo, err := nutrition.StartCycle(s.nutCfg, nutrition.StartCycleInput{
		StrategyID:   linha.ID,
		Index:        0,
		StartDateISO: inicio.Format("2006-01-02"),
	})
	if err != nil {
		return CycleAssessment{}, err
	}

	avaliacao, adaptacao := nutrition.AssessCycle(s.nutCfg, nutrition.AssessCycleInput{
		Strategy:       estrategia,
		Logs:           registosParaMotor(registos),
		PeriodStartISO: inicio.Format(isoComMilesimos),
		NowISO:         agora.Format(isoComMilesimos),
		WeightChangeKg: variacaoDePeso(pesos),
	})
	return CycleAssessment{Cycle: ciclo, Assessment: avaliacao, Adaptation: adaptacao, Strategy: estrategia}, nil
}

// ErrSemEstrategia: ainda não há nada em vigor para avaliar.
var ErrSemEstrategia = errors.New("sem estratégia nutricional em vigor")

const isoComMilesimos = "2006-01-02T15:04:05.000Z"

func estrategiaDaLinha(l repo.StrategyRow) nutrition.Strategy2 {
	return nutrition.Strategy2{
		GoalType:      nutrition.GoalType(l.Goal),
		CalorieTarget: l.CalorieTarget,
		Macros: nutrition.Macros{
			Protein: l.ProteinG, Carbs: l.CarbsG, Fat: l.FatG,
		},
		MealsPerDay: 3,
		// O ajuste face ao gasto é a diferença entre o alvo e o que se estimou
		// gastar: é dele que sai o que o período previa em quilos.
		EnergyAdjustment: l.CalorieTarget - l.TDEEEstimated,
		BasisTDEE:        l.TDEEEstimated,
		BasisSource:      "estimated",
	}
}

func registosParaMotor(linhas []repo.MealLog) []nutrition.Log {
	out := make([]nutrition.Log, 0, len(linhas))
	for _, l := range linhas {
		out = append(out, nutrition.Log{
			RecordedAtISO: l.RecordedAt.UTC().Format(isoComMilesimos),
			Status:        nutrition.LogStatus(l.Status),
			Kcal:          float64(l.Kcal),
			Macros:        nutrition.MacrosFloat{Protein: l.Protein, Carbs: l.Carbs, Fat: l.Fat},
			Portion:       l.Portion,
		})
	}
	return out
}

/*
 * variacaoDePeso é o que o corpo fez no período.
 *
 * Nulo com menos de duas pesagens, e é de propósito: sem duas não há variação,
 * e devolver zero seria dizer "não mudou nada" quando a verdade é "não sei".
 * São duas conclusões opostas — uma manda ajustar o alvo, a outra manda esperar.
 */
func variacaoDePeso(serie []repo.MeasurementEntry) *float64 {
	if len(serie) < 2 {
		return nil
	}
	delta := serie[len(serie)-1].Value - serie[0].Value
	return &delta
}
