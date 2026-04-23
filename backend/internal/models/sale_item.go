package models

import "github.com/google/uuid"

type SaleItem struct {
	BaseModel

	SaleID     uuid.UUID `gorm:"type:uuid;not null;index"`
	MedicineID uuid.UUID `gorm:"type:uuid;not null;index"`

	BatchNo  string
	Quantity int
	Price    float64

	Medicine Medicine
}
