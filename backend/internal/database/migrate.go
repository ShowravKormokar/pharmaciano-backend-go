package database

import (
	"embed"
	"fmt"
	"log"
	"net/url"

	"backend/internal/config"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func RunMigrations() {
	cfg := config.Cfg.DB

	// Build a properly URL-encoded DSN to handle special characters in password
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.Name,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()

	dsn := u.String()

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		log.Fatalf("❌ Failed to create iofs source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dsn)
	if err != nil {
		log.Fatalf("❌ Failed to create migrate instance: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("❌ Migration failed: %v", err)
	}
	log.Println("✅ Database migrations applied")
}
