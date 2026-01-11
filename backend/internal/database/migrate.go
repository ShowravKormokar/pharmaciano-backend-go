package database

import (
	"log"

	"backend/internal/models"
)

func RunMigrations() {
	err := DB.AutoMigrate(
		// Auto migrate all models
		&models.Organization{},
		&models.Branch{},
		&models.Role{},
		&models.User{},
		&models.Category{},
		&models.Brand{},
		&models.Medicine{},
		&models.InventoryBatch{},
		&models.Warehouse{},
		&models.Sale{},
		&models.SaleItem{},
		&models.SalesReturn{},
		&models.ReturnItem{},
		&models.Customer{},
		&models.Supplier{},
		&models.Purchase{},
		&models.PurchaseItem{},
		&models.Account{},
		&models.JournalEntry{},
		&models.Report{},
		&models.AIInsight{},
		&models.Notification{},
		&models.AuditLog{},
		&models.SystemSetting{},
		&models.BackupLog{},
	)

	if err != nil {
		log.Fatal("❌ Migration failed:", err)
	}

	log.Println("✅ Database migration completed")
}
