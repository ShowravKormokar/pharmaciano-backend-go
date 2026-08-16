package db

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

// Migrator wraps golang-migrate with the DSN + embedded FS.
type Migrator struct {
	m   *migrate.Migrate
	log *zap.Logger
}

// NewMigrator opens a Migrator over the given embedded migrations FS.
func NewMigrator(dsn string, fs embed.FS, root string, log *zap.Logger) (*Migrator, error) {
	src, err := iofs.New(fs, root)
	if err != nil {
		return nil, fmt.Errorf("migrator source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return nil, fmt.Errorf("migrator open: %w", err)
	}
	return &Migrator{m: m, log: log}, nil
}

// Up applies all pending migrations. Returns nil if already up to date.
func (m *Migrator) Up() error {
	if err := m.m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	v, dirty, _ := m.m.Version()
	m.log.Info("migrations applied", zap.Uint("version", uint(v)), zap.Bool("dirty", dirty))

	return nil
}

// Down rolls back exactly one migration.
func (m *Migrator) Down(steps int) error {
	if steps <= 0 {
		steps = 1
	}

	if err := m.m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down %d: %w", steps, err)
	}
	return nil
}

// Force sets a specific version — recovery only, never in normal ops.
func (m *Migrator) Force(version int) error {
	return m.m.Force(version)
}

// Version reports the current schema version.
func (m *Migrator) Version() (uint, bool, error) {
	v, dirty, err := m.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return v, dirty, err
}

// Close releases the migrator's DB connection. Errors are logged, not returned, because Close is typically called from defer.
func (m *Migrator) Close() {
	srcErr, dbErr := m.m.Close()
	if srcErr != nil {
		m.log.Warn("migrator source close", zap.Error(srcErr))
	}
	if dbErr != nil {
		m.log.Warn("migrator db close", zap.Error(dbErr))
	}
}

// EnsureUpAtBoot is an optional helper: applies pending migrations if the config flag says so. Not used in production — production uses cmd/migrate.
func (m *Migrator) EnsureUpAtBoot(ctx context.Context, enabled bool) error {
	if !enabled {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return m.Up()
}

// Compile-time guard: keep the pgx driver import in the binary.
// var _ = pgx5.WithConnection
