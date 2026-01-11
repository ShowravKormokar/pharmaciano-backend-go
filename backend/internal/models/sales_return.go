package models

import "time"

type SalesReturn struct {
	ID             uint         `gorm:"primaryKey"`
	SaleID         uint         `gorm:"index;not null"`
	Items          []ReturnItem `gorm:"foreignKey:SalesReturnID"`
	Reason         string
	RefundedAmount float64 `gorm:"not null"`
	ProcessedBy    uint    `gorm:"index;not null"`
	CreatedAt      time.Time

	// Relations
	Sale      Sale `gorm:"foreignKey:SaleID"`
	Processor User `gorm:"foreignKey:ProcessedBy"`
}

type ReturnItem struct {
	ID            uint    `gorm:"primaryKey"`
	SalesReturnID uint    `gorm:"index;not null"`
	MedicineID    uint    `gorm:"index;not null"`
	BatchNo       string  `gorm:"not null"`
	Quantity      int     `gorm:"not null"`
	ReturnPrice   float64 `gorm:"not null"`

	// Relations
	SalesReturn SalesReturn `gorm:"foreignKey:SalesReturnID"`
	Medicine    Medicine    `gorm:"foreignKey:MedicineID"`
}
