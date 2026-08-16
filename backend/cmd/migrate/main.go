package main

import (
	"fmt"
	"os"

	"backend/internal/platform/config"
	"backend/internal/platform/db"
	"backend/internal/platform/logger"
	"backend/migrations"
)

func main() {
	cfg := config.MustLoad(config.LoadOptions{})
	log, err := logger.New(cfg.Logging, cfg.App.Name, cfg.App.Version)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	m, err := db.NewMigrator(cfg.DSN(), migrations.FS, ".", log)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer m.Close()

	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "up":
		if err := m.Up(); err != nil {
			log.Fatal(err.Error())
		}
	case "down":
		if err := m.Down(1); err != nil {
			log.Fatal(err.Error())
		}
	case "version":
		v, dirty, _ := m.Version()
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
	}
}
