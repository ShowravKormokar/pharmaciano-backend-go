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
	cfg := config.Cfg

	var dsn string

	// =========================
	// PRODUCTION (Neon)
	// =========================
	if cfg.AppEnv == "production" {

		dsn = cfg.DB.URL

		log.Println("🚀 Using Neon PostgreSQL for migrations")

	} else {

		// =========================
		// DEVELOPMENT (Local)
		// =========================

		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(cfg.DB.User, cfg.DB.Password),
			Host:   fmt.Sprintf("%s:%d", cfg.DB.Host, cfg.DB.Port),
			Path:   cfg.DB.Name,
		}

		q := u.Query()
		q.Set("sslmode", "disable")

		u.RawQuery = q.Encode()

		dsn = u.String()

		log.Println("🖥️ Using Local PostgreSQL for migrations")
	}

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		log.Fatalf("❌ Failed to create migration source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance(
		"iofs",
		source,
		dsn,
	)

	if err != nil {
		log.Fatalf("❌ Failed to create migrate instance: %v", err)
	}

	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {

		log.Fatalf("❌ Migration failed: %v", err)
	}

	log.Println("✅ Database migrations applied")
}
