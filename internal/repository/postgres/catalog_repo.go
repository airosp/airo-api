package postgres

import (
	"context"
	"fmt"

	"github.com/airosp/airo-api/internal/engine/training"
)

// CatalogRepo carrega e lê o catálogo de exercícios.
//
// O catálogo vem do JSON embutido no motor, que é o mesmo ficheiro que o cliente
// usa. Ter duas listas — uma no código e outra na base de dados — é ter duas
// verdades sobre o que existe.
type CatalogRepo struct{ tx *TxManager }

func NewCatalogRepo(tx *TxManager) *CatalogRepo { return &CatalogRepo{tx: tx} }

// categoryOf traduz o padrão de movimento para a categoria do esquema.
//
// A taxonomia completa da especificação (13 padrões, `goals[]`, impacto) ainda
// não existe na biblioteca do cliente — é a Lacuna 1. Ao migrar, os campos
// novos ficam com valores conservadores em vez de ficarem vazios: um exercício
// sem dificuldade técnica declarada seria proposto a quem nunca o fez.
func categoryOf(p training.MovementPattern) string {
	switch p {
	case training.Cardio:
		return "cardio"
	case training.Mobility:
		return "mobility"
	case training.Core:
		return "core"
	default:
		return "strength"
	}
}

func impactOf(p training.MovementPattern) string {
	switch p {
	case training.Cardio:
		return "high"
	case training.Mobility:
		return "low"
	default:
		return "moderate"
	}
}

func mechanicsOf(p training.MovementPattern) string {
	switch p {
	case training.Core, training.Mobility:
		return "isolation"
	default:
		return "compound"
	}
}

// SeedExercises carrega o catálogo. Idempotente: correr duas vezes actualiza,
// não duplica — o `slug` é a chave natural.
func (r *CatalogRepo) SeedExercises(ctx context.Context) (int, error) {
	lib, err := training.Library()
	if err != nil {
		return 0, err
	}
	q := r.tx.Q(ctx)

	for _, e := range lib {
		intensity := "moderate"
		if e.Pattern == training.Cardio {
			intensity = "high"
		} else if e.Pattern == training.Mobility {
			intensity = "low"
		}
		_, err := q.Exec(ctx,
			`INSERT INTO exercise (slug, name, category, movement_patterns, primary_muscles,
			                       equipment, difficulty, technical_difficulty, physical_difficulty,
			                       intensity, impact_level, mechanics, measure,
			                       is_warmup, is_cooldown, cue, demo_query)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			 ON CONFLICT (slug) DO UPDATE SET
			   name = EXCLUDED.name, primary_muscles = EXCLUDED.primary_muscles,
			   equipment = EXCLUDED.equipment, cue = EXCLUDED.cue,
			   is_warmup = EXCLUDED.is_warmup, is_cooldown = EXCLUDED.is_cooldown`,
			e.ID, e.Name, categoryOf(e.Pattern), []string{string(e.Pattern)}, e.Muscles,
			e.Equipment, string(e.Level), intensity, impactOf(e.Pattern), mechanicsOf(e.Pattern),
			string(e.Measure), e.Warmup, e.Cooldown, e.Cue, e.DemoQuery)
		if err != nil {
			return 0, fmt.Errorf("carregar exercício %q: %w", e.ID, err)
		}
	}
	return len(lib), nil
}

// ExerciseIDs traduz slugs para os uuids do catálogo.
func (r *CatalogRepo) ExerciseIDs(ctx context.Context, slugs []string) (map[string]string, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT slug, id FROM exercise WHERE slug = ANY($1)`, slugs)
	if err != nil {
		return nil, fmt.Errorf("ler catálogo: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}
