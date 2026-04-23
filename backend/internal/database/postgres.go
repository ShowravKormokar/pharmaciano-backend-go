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

	// Enable UUID / random UUID support
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS \"pgcrypto\";").Error; err != nil {
		log.Fatal("❌ Failed to enable pgcrypto extension:", err)
	}

	// Optionally support uuid_generate_v4() if future migrations use uuid-ossp
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\";").Error; err != nil {
		log.Fatal("❌ Failed to enable uuid-ossp extension:", err)
	}

	// Configure connection pooling
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal("❌ Failed to get DB instance:", err)
	}

	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	DB = db

	log.Println("✅ PostgreSQL connected successfully")
}
