package database

import (
	"log"

	"backend/internal/config"
	"backend/internal/models"
)

func RunMigrations() {
	appEnv := config.Cfg.AppEnv

	if appEnv == "development" {
		err := DB.AutoMigrate(
			// Auto migrate all models
			&models.Organization{},
			&models.Branch{},
			&models.Role{},
			&models.Permission{},
			&models.User{},

			&models.Supplier{},
			&models.Category{},
			&models.Brand{},
			&models.Medicine{},

			&models.Warehouse{},
			&models.InventoryBatch{},

			&models.Customer{},
			&models.Sale{},
			&models.SaleItem{},
			&models.SalesReturn{},

			&models.Purchase{},
			&models.PurchaseItem{},

			&models.Account{},
			&models.JournalEntry{},

			&models.AuditLog{},
			&models.BackupLog{},
			&models.SystemSetting{},
			&models.AIInsight{},
		)

		if err != nil {
			log.Fatal("❌ Migration failed:", err)
		}

		log.Println("✅ Database migration completed (DEV ONLY)")
	} else {
		log.Printf("✅ AppEnv=%s; AutoMigrate disabled. Apply production migrations with golang-migrate.\n", appEnv)
	}
}
