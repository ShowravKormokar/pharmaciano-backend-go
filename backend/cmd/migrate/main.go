package main

import (
	"flag"
	"log"

	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/logger"
	"backend/migrations" // import the embed package

	"go.uber.org/zap"
)

func main() {
	var (
		direction = flag.String("direction", "up", "up or down")
		steps     = flag.Int("steps", 0, "number of steps for down (0 = all)")
		force     = flag.Int("force", -1, "force version")
	)
	flag.Parse()

	// Load config
	cfg := config.MustLoad(config.LoadOptions{})

	// Logger
	logger, err := logger.New(cfg.Logging, cfg.App.Name, cfg.App.Version)
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer logger.Sync()

	// Connect to DB
	dsn := cfg.DSN()

	// Use embedded FS from migrations package
	migrator, err := db.NewMigrator(dsn, migrations.FS, ".", logger)
	if err != nil {
		logger.Fatal("migrator init", zap.Error(err))
	}
	defer migrator.Close()

	if *force >= 0 {
		if err := migrator.Force(*force); err != nil {
			logger.Fatal("force version", zap.Error(err))
		}
		logger.Info("forced version", zap.Int("version", *force))
		return
	}

	switch *direction {
	case "up":
		if err := migrator.Up(); err != nil {
			logger.Fatal("migrate up", zap.Error(err))
		}
	case "down":
		if *steps == 0 {
			logger.Warn("down all migrations – this is destructive")
			if err := migrator.Down(-1); err != nil {
				logger.Fatal("migrate down all", zap.Error(err))
			}
		} else {
			if err := migrator.Down(*steps); err != nil {
				logger.Fatal("migrate down steps", zap.Error(err))
			}
		}
	default:
		logger.Fatal("unknown direction", zap.String("direction", *direction))
	}

	logger.Info("migration completed")
}
