package models

import "time"

type Warehouse struct {
    ID        uint      `gorm:"primaryKey"`
    BranchID  uint      `gorm:"index;not null"`
    Name      string    `gorm:"not null"`
    Location  string    
    Capacity  int       
    CreatedAt time.Time
    
    // Relations
    Branch   Branch            `gorm:"foreignKey:BranchID"`
    Inventory []InventoryBatch
}