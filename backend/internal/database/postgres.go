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

func Connect() {
	cfg := config.Cfg.DB
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=Asia/Dhaka",
		cfg.Host, cfg.User, cfg.Password, cfg.Name, cfg.Port)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		log.Fatalf("❌ Failed to connect PostgreSQL: %v", err)
	}

	// Enable extensions
	db.Exec("CREATE EXTENSION IF NOT EXISTS \"pgcrypto\"")
	db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"")

	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	DB = db
	log.Println("✅ PostgreSQL connected")
}
