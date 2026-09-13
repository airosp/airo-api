package postgres

import (
	"context"
	"fmt"

	"github.com/airosp/airo-api/internal/engine/training"
)

// PreferenceRepo guarda o que a pessoa quer sempre, o que nunca quer, e os
// números que fixou.
//
// Viviam só no telemóvel, e a consequência aparecia no Modo Foco: o servidor
// montava a sessão sem elas, o pacote descrevia outro treino e o ecrã voltava
// ao motor local.
type PreferenceRepo struct{ tx *TxManager }

func NewPreferenceRepo(tx *TxManager) *PreferenceRepo { return &PreferenceRepo{tx: tx} }

type Preferences struct {
	Pinned        []string
	Excluded      []string
	Prescriptions map[string]training.Prescription
}

// Read devolve as preferências de um utilizador.
func (r *PreferenceRepo) Read(ctx context.Context, userID string) (Preferences, error) {
	out := Preferences{
		Pinned:        []string{},
		Excluded:      []string{},
		Prescriptions: map[string]training.Prescription{},
	}

	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT exercise_id, kind, sets, target
		   FROM exercise_preference
		  WHERE user_id = $1
		  ORDER BY exercise_id`, userID)
	if err != nil {
		return out, fmt.Errorf("ler preferências: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, kind string
		var sets, target *int16
		if err := rows.Scan(&id, &kind, &sets, &target); err != nil {
			return out, err
		}
		switch kind {
		case "pinned":
			out.Pinned = append(out.Pinned, id)
		case "excluded":
			out.Excluded = append(out.Excluded, id)
		case "prescribed":
			if sets != nil && target != nil {
				out.Prescriptions[id] = training.Prescription{
					Sets: int(*sets), Target: int(*target),
				}
			}
		}
	}
	return out, rows.Err()
}

// Replace substitui as preferências todas de uma vez.
//
// Substituir e não acrescentar: o cliente manda o **estado**, não uma alteração.
// É o mesmo princípio do `PUT /v1/profile` — mandar duas vezes a mesma coisa dá
// o mesmo resultado, que numa rede fraca acontece sempre.
func (r *PreferenceRepo) Replace(ctx context.Context, userID string, p Preferences) error {
	return r.tx.Do(ctx, func(ctx context.Context) error {
		q := r.tx.Q(ctx)
		if _, err := q.Exec(ctx, `DELETE FROM exercise_preference WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("limpar preferências: %w", err)
		}

		// Excluir ganha a fixar. São contraditórios, e resolver aqui é melhor do
		// que gravar as duas e deixar o motor decidir por ordem de leitura —
		// isso daria resultados diferentes conforme o dia.
		excluidos := make(map[string]bool, len(p.Excluded))
		for _, id := range p.Excluded {
			excluidos[id] = true
		}

		ids := []string{}
		kinds := []string{}
		sets := []*int16{}
		targets := []*int16{}

		add := func(id, kind string, s, t *int16) {
			ids = append(ids, id)
			kinds = append(kinds, kind)
			sets = append(sets, s)
			targets = append(targets, t)
		}

		for _, id := range p.Excluded {
			add(id, "excluded", nil, nil)
		}
		for _, id := range p.Pinned {
			if !excluidos[id] {
				add(id, "pinned", nil, nil)
			}
		}
		for id, pres := range p.Prescriptions {
			if excluidos[id] || pres.Sets < 1 || pres.Target < 1 {
				continue
			}
			s, t := int16(pres.Sets), int16(pres.Target)
			add(id, "prescribed", &s, &t)
		}

		if len(ids) == 0 {
			return nil
		}

		// Um `unnest` e não uma linha por exercício: com trinta preferências
		// eram trinta idas à base de dados, e já houve um `context canceled` a
		// meio de uma transacção por causa disso.
		_, err := q.Exec(ctx,
			`INSERT INTO exercise_preference (user_id, exercise_id, kind, sets, target)
			 SELECT $1, id, kind::exercise_preference_kind, s, t
			   FROM unnest($2::text[], $3::text[], $4::smallint[], $5::smallint[])
			        AS x(id, kind, s, t)`,
			userID, ids, kinds, sets, targets)
		if err != nil {
			return fmt.Errorf("gravar preferências: %w", err)
		}
		return nil
	})
}
