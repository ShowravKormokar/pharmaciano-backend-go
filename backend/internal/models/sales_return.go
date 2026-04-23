package models

import "github.com/google/uuid"

type SalesReturn struct {
	BaseModel

	SaleID         uuid.UUID    `gorm:"type:uuid;not null;index"`
	Items          []ReturnItem `gorm:"foreignKey:SalesReturnID"`
	Reason         string
	RefundedAmount float64
	ProcessedBy    uuid.UUID `gorm:"type:uuid;not null;index"`

	// Relations
	Sale      Sale `gorm:"foreignKey:SaleID"`
	Processor User `gorm:"foreignKey:ProcessedBy"`
}

type ReturnItem struct {
	BaseModel

	SalesReturnID uuid.UUID `gorm:"type:uuid;not null;index"`
	MedicineID    uuid.UUID `gorm:"type:uuid;not null;index"`
	BatchNo       string    `gorm:"not null"`
	Quantity      int       `gorm:"not null"`
	ReturnPrice   float64   `gorm:"not null"`

	// Relations
	SalesReturn SalesReturn
	Medicine    Medicine
}
