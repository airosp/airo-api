package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

/*
 * TrainingPlanRepo escreve o treino planeado de cada dia.
 *
 * ⚠️ `planned_session` era lida e nunca escrita. O `ProfileRepo.PlanLabelOn`
 * procurava lá o rótulo do dia desde sempre, não encontrava nada, e o rótulo
 * caía na derivação a partir dos dias de treino do perfil — que é o mesmo que
 * dizer que **o plano de ontem muda quando se muda os dias de treino hoje**.
 * Quem treinava à segunda, quarta e sexta e passasse a treinar à terça e
 * quinta via o histórico inteiro rodar: os treinos feitos continuavam lá, mas
 * ao lado de um plano que dizia outra coisa.
 *
 * O comentário em `service/profiles.go` dizia-o em voz alta — «o plano gravado
 * manda, quando existe. Ainda não existe: nada escreve `planned_session`».
 * Passa a existir.
 */
type TrainingPlanRepo struct{ tx *TxManager }

func NewTrainingPlanRepo(tx *TxManager) *TrainingPlanRepo { return &TrainingPlanRepo{tx: tx} }

/*
 * SavePlannedSession grava o treino de um dia no plano em vigor.
 *
 * Silenciosamente sem efeito quando não há plano: quem ainda não criou
 * objectivo treina na mesma — o motor monta a sessão a partir do perfil — e
 * não há `plan_id` a que a prender. Fazer disto um erro era recusar o treino a
 * quem ainda não tem jornada.
 *
 * Idempotente por `(plan_id, scheduled_on)`: servir o mesmo dia duas vezes
 * actualiza a linha em vez de a duplicar, e é assim que uma mudança de plano
 * feita hoje chega ao dia de hoje.
 */
func (r *TrainingPlanRepo) SavePlannedSession(
	ctx context.Context, userID string, day time.Time, focus, label string, minutes int,
) error {
	q := r.tx.Q(ctx)

	var planID string
	err := q.QueryRow(ctx,
		`SELECT pl.id FROM plan pl
		   JOIN journey j ON j.id = pl.journey_id
		   JOIN goal g ON g.id = j.goal_id
		  WHERE g.user_id = $1
		    AND pl.effective_from <= $2
		    AND (pl.effective_to IS NULL OR pl.effective_to >= $2)
		    AND pl.superseded_by IS NULL
		  ORDER BY pl.effective_from DESC
		  LIMIT 1`, userID, day).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("procurar plano em vigor: %w", err)
	}

	// Segunda = 0, como em todo o resto da app: o `time.Weekday` do Go começa
	// ao domingo, e a semana da Airo começa à segunda.
	weekday := (int(day.Weekday()) + 6) % 7

	_, err = q.Exec(ctx,
		`INSERT INTO planned_session (plan_id, scheduled_on, weekday, focus, label, minutes)
		 VALUES ($1,$2,$3,$4::session_focus,$5,$6)
		 ON CONFLICT (plan_id, scheduled_on) DO UPDATE SET
		   weekday = EXCLUDED.weekday, focus = EXCLUDED.focus,
		   label = EXCLUDED.label, minutes = EXCLUDED.minutes`,
		planID, day, weekday, focus, label, minutes)
	if err != nil {
		return fmt.Errorf("gravar sessão planeada: %w", err)
	}
	return nil
}
