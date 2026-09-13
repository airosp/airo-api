package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AccountRepo apaga a conta e tudo o que lhe pertence.
//
// "Apaga tudo" tem de querer dizer tudo. O esquema faz o grosso por
// `ON DELETE CASCADE` — perfil, medições, objectivos, jornadas, sessões,
// registos alimentares, preferências, tokens. O que **não** cascateia é o
// trilho de auditoria: `auth_event.user_id` fica a nulo e o número de telefone
// ficava lá. Um número é um identificador; deixá-lo depois de alguém pedir para
// ser esquecido não é um trilho de auditoria, é uma cópia da pessoa.
type AccountRepo struct{ tx *TxManager }

func NewAccountRepo(tx *TxManager) *AccountRepo { return &AccountRepo{tx: tx} }

// PhoneOf devolve o número da conta, para se saber o que anonimizar.
func (r *AccountRepo) PhoneOf(ctx context.Context, userID string) (string, error) {
	var phone string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT phone_e164 FROM app_user WHERE id = $1`, userID).Scan(&phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return phone, err
}

// Delete apaga a conta. Numa transação: ou desaparece tudo, ou nada.
//
// Devolve o número de linhas de auditoria anonimizadas, para o registo do
// servidor poder dizer o que aconteceu sem dizer a quem.
func (r *AccountRepo) Delete(ctx context.Context, userID, phone string) (int64, error) {
	var anonimizadas int64
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		q := r.tx.Q(ctx)

		// Primeiro o trilho: com a conta já apagada, `user_id` é nulo e não há
		// como encontrar as linhas que lhe pertenciam.
		tag, err := q.Exec(ctx,
			`UPDATE auth_event
			    SET phone_e164 = NULL, device_id = NULL, meta = '{}'::jsonb
			  WHERE user_id = $1 OR phone_e164 = $2`, userID, phone)
		if err != nil {
			return fmt.Errorf("anonimizar auditoria: %w", err)
		}
		anonimizadas = tag.RowsAffected()

		// Os desafios pendentes daquele número não têm dono por chave
		// estrangeira — vivem pelo telefone. Sem isto, quem apagasse a conta a
		// meio de uma entrada deixava um desafio válido para trás.
		if _, err := q.Exec(ctx,
			`DELETE FROM otp_challenge WHERE phone_e164 = $1`, phone); err != nil {
			return fmt.Errorf("apagar desafios: %w", err)
		}

		tag, err = q.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, userID)
		if err != nil {
			return fmt.Errorf("apagar conta: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	return anonimizadas, err
}
