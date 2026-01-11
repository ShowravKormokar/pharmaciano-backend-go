package database

import (
	"log"

	"backend/internal/models"
)

func RunMigrations() {
	err := DB.AutoMigrate(
		&models.User{},
	)

	if err != nil {
		log.Fatal("❌ Migration failed:", err)
	}

	log.Println("✅ Database migration completed")
}
