package models

import (
	"time"

	"github.com/google/uuid"
)

type InventoryBatch struct {
	BaseModel

	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index"`
	BranchID       uuid.UUID  `gorm:"type:uuid;not null;index"`
	MedicineID     uuid.UUID  `gorm:"type:uuid;not null;index"`
	WarehouseID    *uuid.UUID `gorm:"type:uuid"`

	BatchNo       string    `gorm:"not null"`
	ExpiryDate    time.Time `gorm:"not null"`
	Quantity      int       `gorm:"not null"`
	PurchasePrice float64   `gorm:"not null"`
	Status        string    `gorm:"default:'active'"`

	Medicine  Medicine
	Warehouse *Warehouse
}
