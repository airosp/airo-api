// Package pgtest levanta um Postgres real para os testes.
//
// **Postgres a sério, não um mock.** Um mock de SQL testa o mock: não apanha um
// `CHECK` que não dispara, uma chave estrangeira mal escrita, uma transacção que
// não isola, nem o tipo `jsonb` a recusar o que lhe mandámos. É precisamente
// essa classe de erro que a Fase 1 tem de apanhar.
//
// Usa `embedded-postgres`, que descarrega o binário oficial e o corre em
// processo. Escolhido por não precisar de Docker: a máquina onde isto foi
// escrito não o tem, e um teste que só corre em metade das máquinas não é um
// teste — é uma intenção.
package pgtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	embedded "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	once     sync.Once
	shared   *embedded.EmbeddedPostgres
	baseURL  string
	startErr error
)

// freePort pede ao sistema uma porta livre. Fixar uma faria os testes falhar na
// máquina de quem tivesse um Postgres a correr.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func start() {
	port, err := freePort()
	if err != nil {
		startErr = err
		return
	}
	runtime := filepath.Join(os.TempDir(), "airo-pgtest")

	shared = embedded.NewDatabase(embedded.DefaultConfig().
		Version(embedded.V16).
		Port(uint32(port)).
		Username("airo").
		Password("airo").
		Database("airo_test").
		RuntimePath(runtime).
		StartTimeout(120 * time.Second))

	if err := shared.Start(); err != nil {
		startErr = fmt.Errorf("arrancar postgres de teste: %w", err)
		return
	}
	baseURL = fmt.Sprintf("postgres://airo:airo@127.0.0.1:%d/airo_test?sslmode=disable", port)
}

// Pool devolve um pool ligado a uma base de dados **própria deste teste**.
//
// Uma base por teste e não um `TRUNCATE` entre eles: os testes correm em
// paralelo, e partilhar tabelas transforma uma falha de isolamento numa falha
// intermitente que ninguém consegue reproduzir.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatalf("postgres de teste indisponível: %v", startErr)
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	name := fmt.Sprintf("t_%d_%s", time.Now().UnixNano()%1e9, sanitize(t.Name()))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("criar base %s: %v", name, err)
	}

	url := fmt.Sprintf("postgres://airo:airo@%s/%s?sslmode=disable", hostPort(), name)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func hostPort() string {
	// baseURL = postgres://airo:airo@127.0.0.1:PORT/airo_test?sslmode=disable
	start := len("postgres://airo:airo@")
	end := start
	for end < len(baseURL) && baseURL[end] != '/' {
		end++
	}
	return baseURL[start:end]
}

func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
		default:
			out = append(out, '_')
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}

// Stop encerra o servidor partilhado. Chamado por TestMain.
func Stop() {
	if shared != nil {
		_ = shared.Stop()
	}
}
