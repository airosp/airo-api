package postgres

import (
	"io/fs"
	"testing"

	"github.com/airosp/airo-api/migrations"
)

func realMigrations(t *testing.T) fs.FS {
	t.Helper()
	return migrations.FS
}
