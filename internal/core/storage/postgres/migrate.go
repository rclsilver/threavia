package postgres

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/rclsilver/threavia/migrations"
)

// newMigrator builds a migrate.Migrate over the embedded migration files.
func newMigrator(cfg Config) (*migrate.Migrate, error) {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, cfg.MigrationURL())
	if err != nil {
		return nil, fmt.Errorf("initialise migrations: %w", err)
	}
	return m, nil
}

// MigrateUp applies every pending migration. It is safe to call on every Core
// startup: already-applied migrations are a no-op.
func MigrateUp(cfg Config, logger *slog.Logger) error {
	m, err := newMigrator(cfg)
	if err != nil {
		return err
	}
	defer closeMigrator(m, logger)

	switch err := m.Up(); {
	case err == nil:
		version, dirty, verr := m.Version()
		if verr != nil {
			return fmt.Errorf("read schema version: %w", verr)
		}
		logger.Info("database migrated", "version", version, "dirty", dirty)
		return nil
	case errors.Is(err, migrate.ErrNoChange):
		logger.Debug("database schema already up to date")
		return nil
	default:
		return fmt.Errorf("apply migrations: %w", err)
	}
}

// MigrateDown rolls back every migration. It is destructive and exists for local
// development and tests only.
func MigrateDown(cfg Config, logger *slog.Logger) error {
	m, err := newMigrator(cfg)
	if err != nil {
		return err
	}
	defer closeMigrator(m, logger)

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("roll back migrations: %w", err)
	}
	return nil
}

// SchemaVersion reports the currently applied migration version and whether the
// schema is in a dirty state after a failed migration.
func SchemaVersion(cfg Config, logger *slog.Logger) (version uint, dirty bool, err error) {
	m, merr := newMigrator(cfg)
	if merr != nil {
		return 0, false, merr
	}
	defer closeMigrator(m, logger)

	version, dirty, err = m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

func closeMigrator(m *migrate.Migrate, logger *slog.Logger) {
	sourceErr, dbErr := m.Close()
	if sourceErr != nil {
		logger.Warn("closing migration source", "error", sourceErr)
	}
	if dbErr != nil {
		logger.Warn("closing migration database connection", "error", dbErr)
	}
}
