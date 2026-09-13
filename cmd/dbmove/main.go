//go:build devtools

// dbmove copia uma base de dados para outra.
//
// Existe porque mudar de região não é uma opção na Neon: cria-se um projeto
// novo onde se quer e levam-se os dados. E porque esta máquina não tem
// `pg_dump` — usa-se o protocolo de cópia do próprio Postgres, que é o que o
// `pg_dump` também usa por baixo.
//
// O que faz, por esta ordem:
//
//  1. Aplica o esquema no destino, pelas mesmas migrações do servidor. Copiar
//     o esquema com os dados misturaria duas coisas que falham de maneiras
//     diferentes.
//  2. Ordena as tabelas pelas chaves estrangeiras. Copiar por ordem alfabética
//     rebenta na primeira referência a uma linha que ainda não existe.
//  3. Copia tabela a tabela e conta as linhas dos dois lados.
//
// ⚠️ **Não apaga nada na origem.** A origem fica intacta e continua a servir
// até alguém trocar a variável de ambiente — que é uma decisão separada, e
// deliberadamente manual.
//
//	AIRO_DB_FROM=… AIRO_DB_TO=… go run -tags devtools ./cmd/dbmove [--aplicar]
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	airopg "github.com/airosp/airo-api/internal/platform/postgres"
	"github.com/airosp/airo-api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	origem := os.Getenv("AIRO_DB_FROM")
	destino := os.Getenv("AIRO_DB_TO")
	if origem == "" || destino == "" {
		fmt.Fprintln(os.Stderr, "faltam AIRO_DB_FROM e AIRO_DB_TO")
		os.Exit(1)
	}
	aplicar := len(os.Args) > 1 && os.Args[1] == "--aplicar"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	de, err := pgxpool.New(ctx, origem)
	if err != nil {
		morrer("ligar à origem", err)
	}
	defer de.Close()

	para, err := pgxpool.New(ctx, destino)
	if err != nil {
		morrer("ligar ao destino", err)
	}
	defer para.Close()

	if err := de.Ping(ctx); err != nil {
		morrer("origem inacessível", err)
	}
	if err := para.Ping(ctx); err != nil {
		morrer("destino inacessível", err)
	}

	tabelas, err := ordenarPorDependencia(ctx, de)
	if err != nil {
		morrer("ordenar tabelas", err)
	}

	fmt.Printf("%d tabelas, por ordem de dependência\n", len(tabelas))
	if !aplicar {
		fmt.Println("\nensaio — nada é escrito. Usa --aplicar para copiar.")
		for _, t := range tabelas {
			n, err := contar(ctx, de, t)
			if err != nil {
				morrer("contar "+t, err)
			}
			if n > 0 {
				fmt.Printf("  %-28s %6d linhas\n", t, n)
			}
		}
		return
	}

	// O esquema primeiro, pelas mesmas migrações do servidor.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	migs, err := airopg.Load(migrations.FS)
	if err != nil {
		morrer("ler migrações", err)
	}
	fmt.Println("\na aplicar o esquema no destino…")
	if err := airopg.Up(ctx, para, migs, log); err != nil {
		morrer("migrar destino", err)
	}

	fmt.Println("\na copiar:")
	inicio := time.Now()
	var total int64
	for _, t := range tabelas {
		n, err := copiar(ctx, de, para, t)
		if err != nil {
			morrer("copiar "+t, err)
		}
		total += n
		if n > 0 {
			fmt.Printf("  %-28s %6d linhas\n", t, n)
		}
	}
	fmt.Printf("\n%d linhas em %s\n", total, time.Since(inicio).Round(time.Second))

	// A verificação é o que separa "copiei" de "está lá".
	fmt.Println("\na verificar:")
	problemas := 0
	for _, t := range tabelas {
		a, err := contar(ctx, de, t)
		if err != nil {
			morrer("contar origem "+t, err)
		}
		b, err := contar(ctx, para, t)
		if err != nil {
			morrer("contar destino "+t, err)
		}
		if a != b {
			fmt.Printf("  ✗ %-26s origem %d ≠ destino %d\n", t, a, b)
			problemas++
		}
	}
	if problemas > 0 {
		fmt.Fprintf(os.Stderr, "\n%d tabelas diferentes — **não** troques a variável\n", problemas)
		os.Exit(1)
	}
	fmt.Println("  todas as tabelas com a mesma contagem")
	fmt.Println("\nA origem continua intacta. Trocar AIRO_DATABASE_URL é o passo seguinte, à mão.")
}

// ordenarPorDependencia devolve as tabelas de forma a que nenhuma seja copiada
// antes daquelas a que se refere.
//
// `schema_migration` fica de fora: é do executor de migrações, e o destino
// já a preencheu ao aplicar o esquema. Copiá-la por cima dá violação de chave
// — que foi o que aconteceu à primeira, por eu ter escrito o nome no plural.
func ordenarPorDependencia(ctx context.Context, db *pgxpool.Pool) ([]string, error) {
	rows, err := db.Query(ctx,
		`SELECT tablename FROM pg_tables
		  WHERE schemaname = 'public' AND tablename <> 'schema_migration'
		  ORDER BY tablename`)
	if err != nil {
		return nil, err
	}
	var tabelas []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return nil, err
		}
		tabelas = append(tabelas, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	depende := map[string]map[string]bool{}
	for _, t := range tabelas {
		depende[t] = map[string]bool{}
	}

	fks, err := db.Query(ctx,
		`SELECT c.conrelid::regclass::text, c.confrelid::regclass::text
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE c.contype = 'f' AND n.nspname = 'public'`)
	if err != nil {
		return nil, err
	}
	for fks.Next() {
		var filha, mae string
		if err := fks.Scan(&filha, &mae); err != nil {
			fks.Close()
			return nil, err
		}
		filha, mae = semEsquema(filha), semEsquema(mae)
		// Uma tabela que se refere a si própria (replaced_by, por exemplo) não
		// é uma dependência: as linhas entram todas na mesma cópia.
		if filha != mae && depende[filha] != nil {
			depende[filha][mae] = true
		}
	}
	fks.Close()
	if err := fks.Err(); err != nil {
		return nil, err
	}

	var ordenadas []string
	feitas := map[string]bool{}
	for len(ordenadas) < len(tabelas) {
		progrediu := false
		for _, t := range tabelas {
			if feitas[t] {
				continue
			}
			pronta := true
			for m := range depende[t] {
				if !feitas[m] {
					pronta = false
					break
				}
			}
			if pronta {
				ordenadas = append(ordenadas, t)
				feitas[t] = true
				progrediu = true
			}
		}
		if !progrediu {
			// Um ciclo de chaves estrangeiras. Copiam-se as que restam pela
			// ordem que houver e diz-se — em silêncio seria uma falha no meio
			// da cópia sem explicação.
			for _, t := range tabelas {
				if !feitas[t] {
					fmt.Fprintf(os.Stderr, "aviso: %s está num ciclo de chaves estrangeiras\n", t)
					ordenadas = append(ordenadas, t)
					feitas[t] = true
				}
			}
		}
	}
	sort.SliceStable(ordenadas, func(i, j int) bool { return false })
	return ordenadas, nil
}

func semEsquema(v string) string {
	if i := strings.LastIndex(v, "."); i >= 0 {
		return v[i+1:]
	}
	return strings.Trim(v, `"`)
}

func contar(ctx context.Context, db *pgxpool.Pool, tabela string) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{tabela}.Sanitize()).Scan(&n)
	return n, err
}

// copiar lê todas as linhas e escreve-as com o protocolo de cópia.
func copiar(ctx context.Context, de, para *pgxpool.Pool, tabela string) (int64, error) {
	rows, err := de.Query(ctx, `SELECT * FROM `+pgx.Identifier{tabela}.Sanitize())
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	campos := rows.FieldDescriptions()
	colunas := make([]string, 0, len(campos))
	for _, f := range campos {
		colunas = append(colunas, f.Name)
	}

	var linhas [][]any
	for rows.Next() {
		v, err := rows.Values()
		if err != nil {
			return 0, err
		}
		linhas = append(linhas, v)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(linhas) == 0 {
		return 0, nil
	}

	return para.CopyFrom(ctx, pgx.Identifier{tabela}, colunas, pgx.CopyFromRows(linhas))
}

func morrer(passo string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", passo, err)
	os.Exit(1)
}
