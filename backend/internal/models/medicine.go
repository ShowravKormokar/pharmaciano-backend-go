package models

import "time"

type Medicine struct {
    ID                     uint      `gorm:"primaryKey"`
    Name                   string    `gorm:"not null"`
    GenericName            string    
    CategoryID             uint      `gorm:"index"`
    BrandID                uint      `gorm:"index"`
    DosageForm             string    
    Strength               string    
    Unit                   string    
    MRP                    float64   `gorm:"not null"`
    IsPrescriptionRequired bool      `gorm:"default:false"`
    TaxRate                float64   `gorm:"default:0"`
    CreatedAt              time.Time
    
    // Relations
    Category    Category        `gorm:"foreignKey:CategoryID"`
    Brand       Brand           `gorm:"foreignKey:BrandID"`
    Inventory   []InventoryBatch
    SaleItems   []SaleItem
    PurchaseItems []PurchaseItem
}