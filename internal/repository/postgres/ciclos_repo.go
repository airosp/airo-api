package postgres

import (
	"context"
	"fmt"
	"time"
)

/*
 * CiclosRepo vira os ciclos das jornadas de horizonte aberto.
 *
 * ⚠️ Um ciclo nascia com a jornada e **nunca acabava**: o `closed_at` da tabela
 * nunca foi escrito por ninguém, e o índice do ciclo era recalculado à leitura
 * a partir da data de início. Enquanto a pessoa abrisse a app, ninguém dava
 * pela diferença; quem passasse duas semanas sem abrir voltava a um plano
 * parado no tempo, com um ciclo por fechar e nenhuma revisão feita.
 *
 * Isto corre no worker, fora do pedido de quem quer que seja: a jornada avança
 * porque o tempo passou, e não porque alguém tocou no ecrã.
 */
type CiclosRepo struct{ tx *TxManager }

func NewCiclosRepo(tx *TxManager) *CiclosRepo { return &CiclosRepo{tx: tx} }

// ViragemDeCiclos é o que aconteceu numa passagem.
type ViragemDeCiclos struct {
	Fechados int
	Abertos  int
}

/*
 * Rolar fecha os ciclos vencidos e abre os seguintes.
 *
 * **Idempotente**: correr duas vezes seguidas não muda nada na segunda — o que
 * decide é a data de revisão, e um ciclo que já foi fechado deixa de aparecer.
 *
 * Apanha quem está vários ciclos atrasado: quem desapareceu três meses não
 * volta a um ciclo de Junho, volta ao ciclo de hoje. O limite por passagem
 * existe para uma jornada com datas absurdas não segurar o worker para sempre.
 */
const maximoCiclosPorPassagem = 60

func (r *CiclosRepo) Rolar(ctx context.Context, agora time.Time) (ViragemDeCiclos, error) {
	var out ViragemDeCiclos

	for volta := 0; volta < maximoCiclosPorPassagem; volta++ {
		tag, err := r.tx.Q(ctx).Exec(ctx, `
			WITH vencidos AS (
			    SELECT c.id, c.journey_id, c.index, c.review_date,
			           COALESCE(NULLIF(j.cycle_weeks, 0), 4) AS semanas
			      FROM cycle c
			      JOIN journey j ON j.id = c.journey_id
			     WHERE c.closed_at IS NULL
			       AND c.review_date <= $1::date
			       AND j.status = 'active'
			),
			fechados AS (
			    UPDATE cycle SET closed_at = $1
			     WHERE id IN (SELECT id FROM vencidos)
			 RETURNING id
			)
			INSERT INTO cycle (journey_id, index, start_date, review_date)
			SELECT v.journey_id, v.index + 1, v.review_date,
			       v.review_date + (v.semanas * 7)
			  FROM vencidos v
			-- O ciclo seguinte pode já existir de uma passagem anterior que
			-- tenha falhado a meio. Não é erro: é o mesmo ciclo.
			ON CONFLICT (journey_id, index) DO NOTHING`, agora)
		if err != nil {
			return out, fmt.Errorf("virar ciclos: %w", err)
		}
		abertos := int(tag.RowsAffected())
		if abertos == 0 {
			// Também não havia nada para fechar: sai-se antes de contar outra
			// volta. Ver a nota sobre idempotência.
			break
		}
		out.Abertos += abertos
		out.Fechados += abertos
	}
	return out, nil
}

/*
 * CicloActual devolve o ciclo aberto de uma jornada.
 *
 * Existe para quem lê poder usar o que está gravado em vez de recalcular o
 * índice a partir da data de início — duas contas diferentes sobre a mesma
 * coisa acabam sempre por discordar.
 */
func (r *CiclosRepo) CicloActual(ctx context.Context, journeyID string) (indice int, inicio, revisao time.Time, err error) {
	err = r.tx.Q(ctx).QueryRow(ctx,
		`SELECT index, start_date, review_date FROM cycle
		  WHERE journey_id = $1 AND closed_at IS NULL
		  ORDER BY index DESC LIMIT 1`, journeyID).Scan(&indice, &inicio, &revisao)
	return indice, inicio, revisao, err
}
