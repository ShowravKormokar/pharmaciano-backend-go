package models

import "time"

type InventoryBatch struct {
	BaseModel

	OrganizationID uint `gorm:"not null;index"`
	BranchID       uint `gorm:"not null;index"`
	MedicineID     uint `gorm:"not null;index"`
	WarehouseID    *uint

	BatchNo       string    `gorm:"not null"`
	ExpiryDate    time.Time `gorm:"not null"`
	Quantity      int       `gorm:"not null"`
	PurchasePrice float64   `gorm:"not null"`
	Status        string    `gorm:"default:'active'"`

	Medicine  Medicine
	Warehouse *Warehouse
}
