package models

import "github.com/google/uuid"

type PurchaseItem struct {
	BaseModel

	PurchaseID uuid.UUID `gorm:"type:uuid;not null;index"`
	MedicineID uuid.UUID `gorm:"type:uuid;not null;index"`

	BatchNo    string `gorm:"not null"`
	ExpiryDate string
	Quantity   int     `gorm:"not null"`
	UnitCost   float64 `gorm:"not null"`

	Medicine Medicine `gorm:"foreignKey:MedicineID"`
}
