package models

import "time"

type Purchase struct {
	ID             uint           `gorm:"primaryKey"`
	OrganizationID uint           `gorm:"index;not null"`
	BranchID       uint           `gorm:"index;not null"`
	SupplierID     uint           `gorm:"index;not null"`
	PurchaseNo     string         `gorm:"unique;not null"`
	Items          []PurchaseItem `gorm:"foreignKey:PurchaseID"`
	Status         string         `gorm:"default:'pending'"`
	ApprovedBy     *uint          `gorm:"index"`
	CreatedAt      time.Time

	// Relations
	Organization Organization `gorm:"foreignKey:OrganizationID"`
	Branch       Branch       `gorm:"foreignKey:BranchID"`
	Supplier     Supplier     `gorm:"foreignKey:SupplierID"`
	Approver     *User        `gorm:"foreignKey:ApprovedBy"`
}

type PurchaseItem struct {
	ID         uint      `gorm:"primaryKey"`
	PurchaseID uint      `gorm:"index;not null"`
	MedicineID uint      `gorm:"index;not null"`
	BatchNo    string    `gorm:"not null"`
	ExpiryDate time.Time `gorm:"not null"`
	Quantity   int       `gorm:"not null"`
	UnitCost   float64   `gorm:"not null"`

	// Relations
	Purchase Purchase `gorm:"foreignKey:PurchaseID"`
	Medicine Medicine `gorm:"foreignKey:MedicineID"`
}
