package postgres

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/rclsilver/threavia/migrations"
)

// TestEmbeddedMigrationsAreReadable pins that the migration files are embedded
// in the binary and that golang-migrate accepts their naming.
func TestEmbeddedMigrationsAreReadable(t *testing.T) {
	t.Parallel()

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migration is embedded")
	}

	var ups, downs int
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			ups++
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			downs++
		default:
			t.Errorf("unexpected file %q in migrations", entry.Name())
		}
	}
	if ups != downs {
		t.Errorf("every migration needs a rollback: %d up, %d down", ups, downs)
	}

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("golang-migrate rejected the embedded migrations: %v", err)
	}
	defer func() { _ = source.Close() }()

	if _, err := source.First(); err != nil {
		t.Fatalf("reading the first migration version: %v", err)
	}
}
