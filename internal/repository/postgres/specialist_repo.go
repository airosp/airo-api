package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/airosp/airo-api/internal/engine/training"
	"github.com/jackc/pgx/v5"
)

// SpecialistRepo carrega o catálogo e guarda quem cada pessoa convidou.
type SpecialistRepo struct{ tx *TxManager }

func NewSpecialistRepo(tx *TxManager) *SpecialistRepo { return &SpecialistRepo{tx: tx} }

type SpecialistRow struct {
	ID             string
	Name           string
	Role           string
	Headline       string
	Bio            string
	Tags           []string
	Rating         float64
	Clients        int
	ResponseTime   string
	Initials       string
	Gradient       []string
	RecommendedFor []string
}

// Seed carrega o catálogo do JSON embutido. Idempotente pelo slug.
//
// Corre **antes** das aulas: uma aula aponta para um especialista por chave
// estrangeira, e ao contrário a primeira aula não teria onde encaixar.
func (r *SpecialistRepo) Seed(ctx context.Context) (int, error) {
	lista, err := training.Specialists()
	if err != nil {
		return 0, err
	}
	q := r.tx.Q(ctx)

	for _, s := range lista {
		_, err := q.Exec(ctx,
			`INSERT INTO specialist (id, name, role, headline, bio, tags, rating,
			                         clients, response_time, initials, gradient, recommended_for)
			 VALUES ($1,$2,$3::specialist_role,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			 ON CONFLICT (id) DO UPDATE SET
			   name = EXCLUDED.name, role = EXCLUDED.role,
			   headline = EXCLUDED.headline, bio = EXCLUDED.bio,
			   tags = EXCLUDED.tags, rating = EXCLUDED.rating,
			   clients = EXCLUDED.clients, response_time = EXCLUDED.response_time,
			   initials = EXCLUDED.initials, gradient = EXCLUDED.gradient,
			   recommended_for = EXCLUDED.recommended_for`,
			s.ID, s.Name, string(s.Role), s.Headline, s.Bio, listaOuVazia(s.Tags),
			s.Rating, s.Clients, s.ResponseTime, s.Initials,
			listaOuVazia(s.Gradient), listaOuVazia(s.RecommendedFor))
		if err != nil {
			return 0, fmt.Errorf("carregar especialista %q: %w", s.ID, err)
		}
	}
	return len(lista), nil
}

// All devolve o catálogo activo.
func (r *SpecialistRepo) All(ctx context.Context) ([]SpecialistRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, name, role::text, headline, bio, tags, rating, clients,
		        response_time, initials, gradient, recommended_for
		   FROM specialist WHERE active ORDER BY role, name`)
	if err != nil {
		return nil, fmt.Errorf("ler especialistas: %w", err)
	}
	defer rows.Close()

	out := []SpecialistRow{}
	for rows.Next() {
		var s SpecialistRow
		if err := rows.Scan(&s.ID, &s.Name, &s.Role, &s.Headline, &s.Bio, &s.Tags,
			&s.Rating, &s.Clients, &s.ResponseTime, &s.Initials,
			&s.Gradient, &s.RecommendedFor); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Invited devolve os identificadores que a pessoa convidou.
/*
 * Treinador devolve o especialista que a pessoa pôs na equipa como treinador.
 *
 * É o único papel que muda o **treino**: um nutricionista muda o que se come e
 * um coach muda o que se diz, mas nenhum acrescenta séries ao agachamento. Ver
 * `training/metodo.go`.
 *
 * Vazio quando não há — e aí o plano sai como o motor o monta.
 */
func (r *SpecialistRepo) Treinador(ctx context.Context, userID string) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT specialist_id FROM user_specialist
		  WHERE user_id = $1 AND role = 'trainer' LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("ler treinador: %w", err)
	}
	return id, nil
}

func (r *SpecialistRepo) Invited(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT specialist_id FROM user_specialist WHERE user_id = $1 ORDER BY invited_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("ler convites: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

/*
 * Invite substitui a equipa inteira pela lista que veio.
 *
 * Substitui e não acrescenta: o ecrã manda a equipa que a pessoa tem, não a
 * diferença. Acrescentar deixava um especialista dispensado a acompanhar para
 * sempre.
 *
 * ⚠️ **Um por papel.** É a restrição da tabela, e é o que a app já assume: dois
 * treinadores a assinar o mesmo plano é a confusão que o ecrã de escolha existe
 * para evitar. Quando a lista traz dois do mesmo papel, ganha o último — o que
 * a pessoa escolheu por cima.
 */
func (r *SpecialistRepo) Invite(ctx context.Context, userID string, ids []string) error {
	q := r.tx.Q(ctx)
	if _, err := q.Exec(ctx, `DELETE FROM user_specialist WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("limpar equipa: %w", err)
	}
	for _, id := range ids {
		_, err := q.Exec(ctx,
			`INSERT INTO user_specialist (user_id, specialist_id, role)
			 SELECT $1, s.id, s.role FROM specialist s WHERE s.id = $2 AND s.active
			 ON CONFLICT (user_id, role) DO UPDATE SET
			   specialist_id = EXCLUDED.specialist_id, invited_at = now()`,
			userID, id)
		if err != nil {
			return fmt.Errorf("convidar %q: %w", id, err)
		}
	}
	return nil
}

func listaOuVazia(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
