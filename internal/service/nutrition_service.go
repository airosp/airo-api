package service

import (
	"context"
	"errors"
	"time"

	"github.com/airosp/airo-api/internal/engine/goal"
	"github.com/airosp/airo-api/internal/engine/nutrition"
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

type NutritionService struct {
	strategies StrategyReader
	meals      MealPreferences
	nutCfg     nutrition.Config
	goalCfg    goal.Config
}

func NewNutritionService(s StrategyReader, meals MealPreferences, nutCfg nutrition.Config, goalCfg goal.Config) *NutritionService {
	return &NutritionService{strategies: s, meals: meals, nutCfg: nutCfg, goalCfg: goalCfg}
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

// Today monta o plano alimentar do dia.
func (s *NutritionService) Today(ctx context.Context, in NutritionTodayInput) (NutritionToday, error) {
	strategy, stored, err := s.strategyFor(ctx, in)
	if err != nil {
		return NutritionToday{}, err
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
	return NutritionToday{Strategy: strategy, Day: day, FromStoredStrategy: stored, Swapped: trocadas}, nil
}

// strategyFor prefere a decisão gravada e só calcula quando não há nenhuma.
//
// Quem ainda não criou objectivo não fica sem plano — fica com um de manutenção,
// que é a leitura honesta de "ainda não disseste o que queres".
func (s *NutritionService) strategyFor(ctx context.Context, in NutritionTodayInput) (nutrition.Strategy2, bool, error) {
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
		}, true, nil
	case !errors.Is(err, repo.ErrNotFound):
		return nutrition.Strategy2{}, false, err
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
	}), false, nil
}

func sexoDe(s *string) *goal.Sex {
	if s == nil {
		return nil
	}
	v := goal.Sex(*s)
	return &v
}
