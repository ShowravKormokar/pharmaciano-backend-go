package database

// import (
// 	"fmt"
// 	"log"
// 	"time"

// 	"backend/internal/config"

// 	"gorm.io/driver/postgres"
// 	"gorm.io/gorm"
// 	"gorm.io/gorm/logger"
// )

// var DB *gorm.DB

// func Connect() {
// 	cfg := config.Cfg.DB
// 	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=Asia/Dhaka",
// 		cfg.Host, cfg.User, cfg.Password, cfg.Name, cfg.Port)

// 	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
// 		Logger: logger.Default.LogMode(logger.Info),
// 	})
// 	if err != nil {
// 		log.Fatalf("❌ Failed to connect PostgreSQL: %v", err)
// 	}

// 	// Enable extensions
// 	db.Exec("CREATE EXTENSION IF NOT EXISTS \"pgcrypto\"")
// 	db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"")

// 	sqlDB, _ := db.DB()
// 	sqlDB.SetMaxOpenConns(50)
// 	sqlDB.SetMaxIdleConns(10)
// 	sqlDB.SetConnMaxLifetime(30 * time.Minute)
// 	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

// 	DB = db
// 	log.Println("✅ PostgreSQL connected")
// }
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

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=Asia/Dhaka",
		cfg.DB.Host, cfg.DB.User, cfg.DB.Password, cfg.DB.Name, cfg.DB.Port,
	)

	// ✅ Set log level to WARN to hide SQL queries
	logLevel := logger.Warn
	if config.Cfg.AppEnv == "development" {
		logLevel = logger.Warn // change to logger.Info if you want to see SQL in dev
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.New(
			log.New(log.Writer(), "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  logLevel, // <-- key change
				IgnoreRecordNotFoundError: true,
				Colorful:                  false,
			},
		),
	})

	if err != nil {
		log.Fatal("❌ Failed to connect PostgreSQL:", err)
	}

	// Enable UUID extensions
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
