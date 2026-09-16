package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// PantryRepo guarda o que é da pessoa e não do catálogo: os alimentos que ela
// escreveu e as combinações que guardou.
//
// Juntos no mesmo repositório porque são a mesma pergunta — "o que é meu, à
// mesa" — e porque descem e sobem sempre ao mesmo tempo.
type PantryRepo struct{ tx *TxManager }

func NewPantryRepo(tx *TxManager) *PantryRepo { return &PantryRepo{tx: tx} }

type CustomFoodRow struct {
	ID        string
	Name      string
	Kcal      float64
	ProteinG  float64
	CarbsG    float64
	FatG      float64
	ServingG  int
	CreatedAt time.Time
}

type FavouriteRow struct {
	ID      string
	Slot    string
	Title   string
	Items   json.RawMessage
	Kcal    int
	SavedAt time.Time
}

// ── alimentos próprios ──────────────────────────────────────────────────────

func (r *PantryRepo) CustomFoods(ctx context.Context, userID string) ([]CustomFoodRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, name, kcal, protein_g, carbs_g, fat_g, serving_g, created_at
		   FROM custom_food WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("ler alimentos próprios: %w", err)
	}
	defer rows.Close()

	out := []CustomFoodRow{}
	for rows.Next() {
		var f CustomFoodRow
		if err := rows.Scan(&f.ID, &f.Name, &f.Kcal, &f.ProteinG, &f.CarbsG,
			&f.FatG, &f.ServingG, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SaveCustomFood grava um alimento. Idempotente pelo identificador: reenviar o
// mesmo alimento é reenviar o mesmo alimento.
func (r *PantryRepo) SaveCustomFood(ctx context.Context, userID string, f CustomFoodRow) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO custom_food (id, user_id, name, kcal, protein_g, carbs_g, fat_g, serving_g)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name, kcal = EXCLUDED.kcal,
		   protein_g = EXCLUDED.protein_g, carbs_g = EXCLUDED.carbs_g,
		   fat_g = EXCLUDED.fat_g, serving_g = EXCLUDED.serving_g
		 -- Sem esta cláusula, adivinhar um identificador reescrevia o alimento
		 -- de outra pessoa.
		 WHERE custom_food.user_id = $2`,
		f.ID, userID, f.Name, f.Kcal, f.ProteinG, f.CarbsG, f.FatG, f.ServingG)
	if err != nil {
		return fmt.Errorf("gravar alimento próprio: %w", err)
	}
	return nil
}

func (r *PantryRepo) DeleteCustomFood(ctx context.Context, userID, id string) (bool, error) {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM custom_food WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, fmt.Errorf("apagar alimento próprio: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ── favoritas ───────────────────────────────────────────────────────────────

func (r *PantryRepo) Favourites(ctx context.Context, userID string) ([]FavouriteRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, slot::text, title, items, kcal, saved_at
		   FROM favourite_meal WHERE user_id = $1 ORDER BY saved_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("ler favoritas: %w", err)
	}
	defer rows.Close()

	out := []FavouriteRow{}
	for rows.Next() {
		var f FavouriteRow
		if err := rows.Scan(&f.ID, &f.Slot, &f.Title, &f.Items, &f.Kcal, &f.SavedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *PantryRepo) SaveFavourite(ctx context.Context, userID string, f FavouriteRow) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO favourite_meal (id, user_id, slot, title, items, kcal, saved_at)
		 VALUES ($1,$2,$3::meal_slot,$4,$5,$6,COALESCE($7, now()))
		 ON CONFLICT (id) DO UPDATE SET
		   slot = EXCLUDED.slot, title = EXCLUDED.title,
		   items = EXCLUDED.items, kcal = EXCLUDED.kcal
		 WHERE favourite_meal.user_id = $2`,
		f.ID, userID, f.Slot, f.Title, f.Items, f.Kcal, nuloSeVazio(f.SavedAt))
	if err != nil {
		return fmt.Errorf("gravar favorita: %w", err)
	}
	return nil
}

func (r *PantryRepo) DeleteFavourite(ctx context.Context, userID, id string) (bool, error) {
	tag, err := r.tx.Q(ctx).Exec(ctx,
		`DELETE FROM favourite_meal WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, fmt.Errorf("apagar favorita: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// nuloSeVazio deixa o `now()` da base decidir quando o cliente não manda data.
func nuloSeVazio(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
