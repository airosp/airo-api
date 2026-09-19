package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/jackc/pgx/v5"
)

/*
 * NutritionPlanRepo guarda o plano alimentar que foi servido num dia.
 *
 * ⚠️ `daily_plan`, `planned_meal` e `meal_item` existem desde o `0001_init.sql`
 * e nunca receberam uma linha. O dia era montado a cada pedido a partir da
 * estratégia e da data, mostrado, e esquecido.
 *
 * Isso funciona enquanto se pergunta por hoje. Deixa de funcionar no dia
 * seguinte: perguntar por **ontem** remontava o plano com a estratégia de
 * agora e com o perfil de agora, e devolvia um dia que nunca existiu. Quem
 * mudasse o objectivo na quarta via a terça inteira reescrita — as mesmas
 * calorias de hoje, os mesmos pratos de hoje — ao lado de um diário que
 * registava outra coisa. O histórico deixava de ser histórico.
 *
 * O que fica guardado é o que foi servido, com a estratégia que o produziu.
 */
type NutritionPlanRepo struct {
	tx      *TxManager
	catalog *NutritionCatalogRepo
}

func NewNutritionPlanRepo(tx *TxManager) *NutritionPlanRepo {
	return &NutritionPlanRepo{tx: tx, catalog: NewNutritionCatalogRepo(tx)}
}

// StoredDay é um dia como foi servido, e a estratégia que o produziu.
type StoredDay struct {
	StrategyID string
	Plan       nutrition.DayPlan
}

/*
 * SaveDay grava o dia inteiro, substituindo o que lá estivesse.
 *
 * Numa transacção, e apagando as refeições antes de escrever as novas: um dia
 * meio escrito — três refeições das quatro, ou uma refeição sem itens — é pior
 * do que um dia não guardado, porque é lido como se estivesse completo.
 *
 * Os alimentos que o catálogo não conhece são saltados, não recusados. Um
 * plano com uma comida que saiu da lista continua a ser o plano que a pessoa
 * viu; recusar o dia inteiro por causa de um item era perder o resto.
 */
func (r *NutritionPlanRepo) SaveDay(
	ctx context.Context, userID, strategyID string, day time.Time, plan nutrition.DayPlan,
) error {
	slugs := map[string]bool{}
	for _, m := range plan.Meals {
		for _, it := range m.Items {
			slugs[it.FoodID] = true
		}
	}
	lista := make([]string, 0, len(slugs))
	for s := range slugs {
		lista = append(lista, s)
	}

	return r.tx.Do(ctx, func(ctx context.Context) error {
		ids, err := r.catalog.FoodIDs(ctx, lista)
		if err != nil {
			return err
		}
		q := r.tx.Q(ctx)

		var planoID string
		err = q.QueryRow(ctx,
			`INSERT INTO daily_plan (user_id, strategy_id, local_day)
			 VALUES ($1,$2,$3)
			 ON CONFLICT (user_id, local_day) DO UPDATE SET strategy_id = EXCLUDED.strategy_id
			 RETURNING id`,
			userID, strategyID, day).Scan(&planoID)
		if err != nil {
			return fmt.Errorf("gravar plano do dia: %w", err)
		}

		// As refeições vão-se abaixo e voltam. `meal_item` cai com elas por
		// cascata — é o que faz uma troca substituir a refeição em vez de
		// deixar os dois pratos lá dentro.
		if _, err := q.Exec(ctx, `DELETE FROM planned_meal WHERE daily_plan_id = $1`, planoID); err != nil {
			return fmt.Errorf("limpar refeições do dia: %w", err)
		}

		for _, m := range plan.Meals {
			var refeicaoID string
			err := q.QueryRow(ctx,
				`INSERT INTO planned_meal (daily_plan_id, slot, role, title,
				                           kcal_target, protein_g, carbs_g, fat_g)
				 VALUES ($1,$2::meal_slot,$3::meal_role,$4,$5,$6,$7,$8)
				 RETURNING id`,
				planoID, string(m.Slot), m.Role, m.Title, m.Kcal,
				m.Macros.Protein, m.Macros.Carbs, m.Macros.Fat).Scan(&refeicaoID)
			if err != nil {
				return fmt.Errorf("gravar refeição %q: %w", m.Slot, err)
			}

			// A posição conta-se à parte do índice do item: um alimento que o
			// catálogo não conhece é saltado, e um buraco na posição violava
			// o `UNIQUE (planned_meal_id, position)` na leitura seguinte.
			posicao := 0
			for _, it := range m.Items {
				foodID, ok := ids[it.FoodID]
				if !ok {
					continue
				}
				_, err := q.Exec(ctx,
					`INSERT INTO meal_item (planned_meal_id, food_id, grams, position)
					 VALUES ($1,$2,$3,$4)`,
					refeicaoID, foodID, int(it.Grams), posicao)
				if err != nil {
					return fmt.Errorf("gravar item %q: %w", it.FoodID, err)
				}
				posicao++
			}
		}
		return nil
	})
}

/*
 * Day lê o dia guardado.
 *
 * Os valores dos itens — nome, calorias, macros — **não** são lidos da base:
 * recalculam-se do catálogo a partir dos gramas, pela mesma função que o motor
 * usa. Guardá-los seria guardar uma segunda verdade sobre o que é 120 g de
 * frango, e as duas divergiriam no dia em que o catálogo fosse corrigido.
 *
 * Devolve `ErrNotFound` quando o dia não foi guardado — que é o normal para
 * todos os dias anteriores a esta funcionalidade existir.
 */
func (r *NutritionPlanRepo) Day(ctx context.Context, userID string, day time.Time) (StoredDay, error) {
	q := r.tx.Q(ctx)

	var planoID, estrategiaID string
	var alvo, proteinaDia, hidratosDia, gorduraDia int
	err := q.QueryRow(ctx,
		`SELECT dp.id, dp.strategy_id, ns.calorie_target, ns.protein_g, ns.carbs_g, ns.fat_g
		   FROM daily_plan dp
		   JOIN nutrition_strategy ns ON ns.id = dp.strategy_id
		  WHERE dp.user_id = $1 AND dp.local_day = $2`,
		userID, day).Scan(&planoID, &estrategiaID, &alvo, &proteinaDia, &hidratosDia, &gorduraDia)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredDay{}, ErrNotFound
	}
	if err != nil {
		return StoredDay{}, fmt.Errorf("ler plano do dia: %w", err)
	}

	rows, err := q.Query(ctx,
		`SELECT pm.slot::text, pm.role::text, pm.title, pm.kcal_target,
		        pm.protein_g, pm.carbs_g, pm.fat_g,
		        coalesce(f.slug, ''), coalesce(mi.grams, 0)
		   FROM planned_meal pm
		   LEFT JOIN meal_item mi ON mi.planned_meal_id = pm.id
		   LEFT JOIN food f ON f.id = mi.food_id
		  WHERE pm.daily_plan_id = $1
		  ORDER BY pm.slot, mi.position`, planoID)
	if err != nil {
		return StoredDay{}, fmt.Errorf("ler refeições do dia: %w", err)
	}
	defer rows.Close()

	dia := day.Format("2006-01-02")
	porSlot := map[string]int{}
	plano := nutrition.DayPlan{DayISO: dia}

	for rows.Next() {
		var slot, papel, titulo, slug string
		var kcal, gramas int
		var proteina, hidratos, gordura float64
		if err := rows.Scan(&slot, &papel, &titulo, &kcal,
			&proteina, &hidratos, &gordura, &slug, &gramas); err != nil {
			return StoredDay{}, err
		}

		i, visto := porSlot[slot]
		if !visto {
			plano.Meals = append(plano.Meals, nutrition.PlannedMeal{
				// O mesmo identificador que o motor dá: é dele que os ecrãs
				// se lembram, e um id diferente ao reler o dia fazia a app
				// tratar a mesma refeição como outra.
				ID:     fmt.Sprintf("%s-%s", dia, slot),
				Slot:   nutrition.Slot(slot),
				Title:  titulo,
				Role:   papel,
				Kcal:   kcal,
				Macros: nutrition.MacrosFloat{Protein: proteina, Carbs: hidratos, Fat: gordura},
			})
			i = len(plano.Meals) - 1
			porSlot[slot] = i
		}
		// Um `LEFT JOIN` sem itens traz uma linha com slug vazio: é uma
		// refeição sem alimentos, não um alimento sem nome.
		if slug == "" {
			continue
		}
		if item, ok := nutrition.ItemFromFood(slug, float64(gramas)); ok {
			plano.Meals[i].Items = append(plano.Meals[i].Items, item)
		}
	}
	if err := rows.Err(); err != nil {
		return StoredDay{}, err
	}
	if len(plano.Meals) == 0 {
		return StoredDay{}, ErrNotFound
	}

	/*
	 * O total do dia é o da estratégia, e não a soma das refeições.
	 *
	 * É assim que o motor o fecha — `DayPlan.Kcal` é o `CalorieTarget` — e a
	 * diferença não é cosmética: os macros de cada refeição são deslocados
	 * pelo papel (pré-treino leva mais hidratos, pós-treino mais proteína) de
	 * forma a preservar as **calorias**, não as parcelas. Somá-las e arredondar
	 * dava um alvo diário ligeiramente diferente daquele que a pessoa tem.
	 */
	plano.Kcal = alvo
	plano.Macros = nutrition.Macros{Protein: proteinaDia, Carbs: hidratosDia, Fat: gorduraDia}

	return StoredDay{StrategyID: estrategiaID, Plan: plano}, nil
}
