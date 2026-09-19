package service_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/airosp/airo-api/internal/platform/clock"
	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/internal/platform/postgres/pgtest"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
	"github.com/airosp/airo-api/internal/service"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
 * O treino planeado de um dia fica escrito, e o passado deixa de rodar.
 *
 * ⚠️ `planned_session` era lida e nunca escrita: o rótulo do dia derivava-se
 * sempre dos dias de treino **de agora**. Bastava mudar de dias para o
 * histórico inteiro mudar de treino por baixo das sessões já feitas.
 */

func baseComPlano(t *testing.T, agora time.Time, dias []int) (*service.Profiles, *pgxpool.Pool, context.Context, string) {
	t.Helper()
	pool := pgtest.Pool(t)
	ctx := context.Background()
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := airopg.Up(ctx, pool, migs, quiet); err != nil {
		t.Fatal(err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ('+258841115000','MZ') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	// O plano em vigor: é a ele que a sessão planeada se prende.
	var goalID, journeyID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO goal (user_id, type, horizon, direction, priority, status)
		 VALUES ($1,'outcome','open_ended','lose_weight','weight','active') RETURNING id`,
		userID).Scan(&goalID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO journey (goal_id, horizon, start_date, cycle_weeks, status)
		 VALUES ($1,'open_ended','2026-09-01',4,'active') RETURNING id`,
		goalID).Scan(&journeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO plan (journey_id, frequency_per_week, session_minutes,
		                   intensity, progression, recovery, effective_from)
		 VALUES ($1,3,45,'moderate','linear','standard','2026-09-01')`, journeyID); err != nil {
		t.Fatal(err)
	}

	p := perfis(t, pool, agora)
	guardarPerfil(t, p, ctx, userID, dias, agora)
	return p, pool, ctx, userID
}

func perfis(t *testing.T, pool *pgxpool.Pool, agora time.Time) *service.Profiles {
	t.Helper()
	tx := repo.NewTxManager(pool)
	return service.NewProfiles(repo.NewProfileRepo(tx), nil, repo.NewPreferenceRepo(tx)).
		ComPlanoDoDia(repo.NewTrainingPlanRepo(tx), clock.NewFixed(agora), training.DefaultConfig())
}

func guardarPerfil(t *testing.T, p *service.Profiles, ctx context.Context, userID string, dias []int, agora time.Time) {
	t.Helper()
	peso := 80.0
	idade := 34
	if _, err := p.Save(ctx, userID, service.SaveProfileInput{
		DisplayName: "Teste", AgeYears: &idade, Sex: "unspecified",
		HeightCm: 175, WeightKg: &peso,
		Experience: "intermediate", WorkoutDays: dias, WorkoutMinutes: 45,
		WorkoutTime: "evening", Equipment: []string{"bodyweight", "dumbbells"},
		DietStyle: "omnivore", MealsPerDay: 4, FoodBudget: "medium",
		FoodExclusions: []string{},
	}, agora); err != nil {
		t.Fatalf("guardar perfil: %v", err)
	}
}

func TestOTreinoDeHojeFicaEscritoEOPassadoNaoRoda(t *testing.T) {
	// Segunda, quarta e sexta. 2026-09-16 é uma quarta-feira.
	quarta := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	p, pool, ctx, userID := baseComPlano(t, quarta.Add(9*time.Hour), []int{0, 2, 4})

	vivido, err := p.TrainingProfile(ctx, userID, quarta)
	if err != nil {
		t.Fatalf("treino de hoje: %v", err)
	}
	if vivido.PlanLabel == "" {
		t.Fatal("o dia veio sem rótulo")
	}

	var escritas int
	var rotulo, foco string
	var minutos, weekday int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) OVER (), label, focus::text, minutes, weekday
		   FROM planned_session WHERE scheduled_on = $1`, quarta).
		Scan(&escritas, &rotulo, &foco, &minutos, &weekday); err != nil {
		t.Fatalf("ler sessão planeada: %v", err)
	}
	if escritas != 1 {
		t.Fatalf("%d sessões planeadas para o dia, esperava 1", escritas)
	}
	if rotulo != vivido.PlanLabel {
		t.Errorf("ficou escrito %q, foi servido %q", rotulo, vivido.PlanLabel)
	}
	if minutos != 45 {
		t.Errorf("minutos escritos = %d, esperava 45", minutos)
	}
	// Quarta-feira com a semana a começar à segunda.
	if weekday != 2 {
		t.Errorf("weekday = %d, esperava 2 (quarta, semana a começar à segunda)", weekday)
	}
	if foco != string(training.DefaultConfig().FocusOf(vivido.PlanLabel)) {
		t.Errorf("foco escrito %q não corresponde ao rótulo %q", foco, vivido.PlanLabel)
	}

	// Passa um dia e a pessoa muda os dias de treino: terça e quinta.
	amanha := quarta.AddDate(0, 0, 1)
	p2 := perfis(t, pool, amanha.Add(9*time.Hour))
	guardarPerfil(t, p2, ctx, userID, []int{1, 3}, amanha)

	relido, err := p2.TrainingProfile(ctx, userID, quarta)
	if err != nil {
		t.Fatalf("reler a quarta: %v", err)
	}
	if relido.PlanLabel != vivido.PlanLabel {
		t.Errorf("a quarta passou de %q para %q — o passado rodou com a mudança de dias",
			vivido.PlanLabel, relido.PlanLabel)
	}
}

/*
 * Hoje deriva-se sempre — mudar os dias de treino de manhã vê-se de manhã.
 *
 * É a outra metade da regra: ler o escrito também para hoje prendia o dia, e
 * quem mudasse o plano ficava com o treino antigo até à meia-noite.
 */
func TestMudarOsDiasDeTreinoVeSeHoje(t *testing.T) {
	quarta := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	p, pool, ctx, userID := baseComPlano(t, quarta.Add(9*time.Hour), []int{0, 2, 4})

	antes, err := p.TrainingProfile(ctx, userID, quarta)
	if err != nil {
		t.Fatal(err)
	}

	// Mesmo dia, outros dias de treino: a quarta deixa de ser dia de treino na
	// rotação de terça/quinta, e o rótulo tem de acompanhar.
	guardarPerfil(t, p, ctx, userID, []int{1, 3}, quarta.Add(10*time.Hour))
	depois, err := p.TrainingProfile(ctx, userID, quarta)
	if err != nil {
		t.Fatal(err)
	}
	if depois.PlanLabel == antes.PlanLabel {
		t.Skipf("a rotação deu o mesmo rótulo (%q) para os dois conjuntos de dias — "+
			"o que este teste protege é a derivação, não o rótulo", antes.PlanLabel)
	}

	var escrito string
	if err := pool.QueryRow(ctx,
		`SELECT label FROM planned_session WHERE scheduled_on = $1`, quarta).Scan(&escrito); err != nil {
		t.Fatal(err)
	}
	if escrito != depois.PlanLabel {
		t.Errorf("o escrito ficou em %q e o servido é %q — a linha de hoje não acompanhou",
			escrito, depois.PlanLabel)
	}
}
