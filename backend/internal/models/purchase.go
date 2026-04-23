package models

import "github.com/google/uuid"

type Purchase struct {
	BaseModel

	OrganizationID uuid.UUID
	BranchID       uuid.UUID
	SupplierID     uuid.UUID

	PurchaseNo string `gorm:"unique;not null"`
	Status     string `gorm:"default:'pending'"`

	Items []PurchaseItem `gorm:"foreignKey:PurchaseID;constraint:OnDelete:CASCADE"`
}
