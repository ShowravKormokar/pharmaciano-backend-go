package models

import "github.com/google/uuid"

type Sale struct {
	BaseModel

	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index"`
	BranchID       uuid.UUID `gorm:"type:uuid;not null;index"`
	InvoiceNo      string    `gorm:"unique;not null"`

	CashierID  *uuid.UUID
	CustomerID *uuid.UUID

	Subtotal    float64
	Discount    float64
	Tax         float64
	TotalAmount float64
	PaymentType string

	Items []SaleItem
}
