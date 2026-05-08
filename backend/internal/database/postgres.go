package database

import (
	"fmt"
	"log"
	"time"

	"backend/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func ConnectPostgres() {
	cfg := config.Cfg

	var dsn string

	// PRODUCTION (Neon)
	if cfg.AppEnv == "production" {
		dsn = cfg.DB.URL
	} else {


		// DEVELOPMENT (Local)

		dsn = fmt.Sprintf(
			"host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=Asia/Dhaka",
			cfg.DB.Host,
			cfg.DB.User,
			cfg.DB.Password,
			cfg.DB.Name,
			cfg.DB.Port,
		)
	}

	logLevel := logger.Warn

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})

	if err != nil {
		log.Fatal("❌ PostgreSQL connection failed:", err)
	}

	sqlDB, _ := db.DB()

	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	db.Exec(`CREATE EXTENSION IF NOT EXISTS "pgcrypto"`)

	DB = db

	log.Println("✅ PostgreSQL connected")
}
