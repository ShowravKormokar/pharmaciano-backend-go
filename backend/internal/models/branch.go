package models

import "time"

type Branch struct {
    ID             uint      `gorm:"primaryKey"`
    OrganizationID uint      `gorm:"index;not null"`
    Name           string    `gorm:"not null"`
    Address        string    
    Phone          string    
    ManagerID      uint      
    IsActive       bool      `gorm:"default:true"`
    CreatedAt      time.Time
    
    // Relations
    Organization Organization `gorm:"foreignKey:OrganizationID"`
    Users        []User
    Inventory    []InventoryBatch
    Sales        []Sale
    Purchases    []Purchase
    Warehouses   []Warehouse
}