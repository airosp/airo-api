package postgres

import (
	"context"
	"fmt"
	"time"
)

/*
 * MediaRepo guarda as imagens já encontradas.
 *
 * ⚠️ Cada telemóvel resolvia as suas: abrir a app eram dezenas de pedidos ao
 * acervo para descobrir o que toda a gente já tinha descoberto. E a quota é da
 * conta inteira, não de cada pessoa — com alguma gente ao mesmo tempo, as
 * fotografias deixavam de aparecer sem nada no ecrã a explicar porquê.
 */
type MediaRepo struct{ tx *TxManager }

func NewMediaRepo(tx *TxManager) *MediaRepo { return &MediaRepo{tx: tx} }

// Imagem é o que ficou guardado sobre um assunto.
type Imagem struct {
	Subject   string
	URL       string
	Width     int
	Height    int
	Credit    string
	FetchedAt time.Time
}

/*
 * Buscar devolve o que já se sabe sobre estes assuntos.
 *
 * Os que não estiverem no mapa nunca foram procurados. Os que estiverem com
 * `URL` vazio foram procurados e não havia — é uma resposta, e guarda-se para
 * não se repetir a busca a cada ecrã.
 */
func (r *MediaRepo) Buscar(ctx context.Context, assuntos []string) (map[string]Imagem, error) {
	out := map[string]Imagem{}
	if len(assuntos) == 0 {
		return out, nil
	}
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT subject, url, COALESCE(width,0), COALESCE(height,0), COALESCE(credit,''), fetched_at
		   FROM media_asset WHERE subject = ANY($1)`, assuntos)
	if err != nil {
		return nil, fmt.Errorf("ler imagens: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var i Imagem
		if err := rows.Scan(&i.Subject, &i.URL, &i.Width, &i.Height, &i.Credit, &i.FetchedAt); err != nil {
			return nil, err
		}
		out[i.Subject] = i
	}
	return out, rows.Err()
}

/*
 * Guardar grava o que se encontrou — ou que não se encontrou nada.
 *
 * Último a escrever ganha: duas pessoas a abrir a app ao mesmo tempo podem
 * procurar o mesmo alimento, e as duas respostas servem igualmente.
 */
func (r *MediaRepo) Guardar(ctx context.Context, i Imagem) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO media_asset (subject, url, width, height, credit, fetched_at)
		 VALUES ($1,$2,NULLIF($3,0),NULLIF($4,0),NULLIF($5,''),now())
		 ON CONFLICT (subject) DO UPDATE
		   SET url = EXCLUDED.url, width = EXCLUDED.width, height = EXCLUDED.height,
		       credit = EXCLUDED.credit, fetched_at = now()`,
		i.Subject, i.URL, i.Width, i.Height, i.Credit)
	if err != nil {
		return fmt.Errorf("guardar imagem: %w", err)
	}
	return nil
}
