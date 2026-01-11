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

// ConnectPostgres initializes PostgreSQL connection
func ConnectPostgres() {
	cfg := config.Cfg

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=Asia/Dhaka",
		cfg.DB.Host,
		cfg.DB.User,
		cfg.DB.Password,
		cfg.DB.Name,
		cfg.DB.Port,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.New(
			log.New(log.Writer(), "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  logger.Info,
				IgnoreRecordNotFoundError: true,
				Colorful:                  true,
			},
		),
	})

	if err != nil {
		log.Fatal("❌ Failed to connect PostgreSQL:", err)
	}

	DB = db

	log.Println("✅ PostgreSQL connected successfully")
}
