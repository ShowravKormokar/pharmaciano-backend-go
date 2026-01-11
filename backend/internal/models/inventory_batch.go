package models

import "time"

type InventoryBatch struct {
    ID             uint      `gorm:"primaryKey"`
    OrganizationID uint      `gorm:"index;not null"`
    BranchID       uint      `gorm:"index;not null"`
    MedicineID     uint      `gorm:"index;not null"`
    BatchNo        string    `gorm:"not null"`
    ExpiryDate     time.Time `gorm:"not null"`
    Quantity       int       `gorm:"not null"`
    PurchasePrice  float64   `gorm:"not null"`
    WarehouseID    uint      `gorm:"index"`
    Status         string    `gorm:"default:'active'"`
    CreatedAt      time.Time
    
    // Relations
    Organization Organization `gorm:"foreignKey:OrganizationID"`
    Branch       Branch       `gorm:"foreignKey:BranchID"`
    Medicine     Medicine     `gorm:"foreignKey:MedicineID"`
    Warehouse    Warehouse    `gorm:"foreignKey:WarehouseID"`
}