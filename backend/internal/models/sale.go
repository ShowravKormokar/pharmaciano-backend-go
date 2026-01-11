package models

import "time"

type Sale struct {
	ID             uint       `gorm:"primaryKey"`
	OrganizationID uint       `gorm:"index;not null"`
	BranchID       uint       `gorm:"index;not null"`
	InvoiceNo      string     `gorm:"unique;not null"`
	CashierID      uint       `gorm:"index;not null"`
	CustomerID     *uint      `gorm:"index"`
	Items          []SaleItem `gorm:"foreignKey:SaleID"`
	Subtotal       float64    `gorm:"not null"`
	Discount       float64    `gorm:"default:0"`
	Tax            float64    `gorm:"default:0"`
	TotalAmount    float64    `gorm:"not null"`
	PaymentMethod  string     `gorm:"not null"`
	CreatedAt      time.Time

	// Relations
	Organization Organization `gorm:"foreignKey:OrganizationID"`
	Branch       Branch       `gorm:"foreignKey:BranchID"`
	Cashier      User         `gorm:"foreignKey:CashierID"`
	Customer     *Customer    `gorm:"foreignKey:CustomerID"`
	Returns      []SalesReturn
}

type SaleItem struct {
	ID           uint    `gorm:"primaryKey"`
	SaleID       uint    `gorm:"index;not null"`
	MedicineID   uint    `gorm:"index;not null"`
	BatchNo      string  `gorm:"not null"`
	Quantity     int     `gorm:"not null"`
	SellingPrice float64 `gorm:"not null"`

	// Relations
	Sale     Sale     `gorm:"foreignKey:SaleID"`
	Medicine Medicine `gorm:"foreignKey:MedicineID"`
}