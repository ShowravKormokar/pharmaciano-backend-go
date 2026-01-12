package models

type Sale struct {
	BaseModel

	OrganizationID uint   `gorm:"not null;index"`
	BranchID       uint   `gorm:"not null;index"`
	InvoiceNo      string `gorm:"unique;not null"`

	CashierID  uint
	CustomerID *uint

	Subtotal    float64
	Discount    float64
	Tax         float64
	TotalAmount float64
	PaymentType string

	Items []SaleItem
}
