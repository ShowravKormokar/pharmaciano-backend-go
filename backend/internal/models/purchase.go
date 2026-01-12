package models

type Purchase struct {
	BaseModel

	OrganizationID uint
	BranchID       uint
	SupplierID     uint

	PurchaseNo string `gorm:"unique;not null"`
	Status     string `gorm:"default:'pending'"`

	Items []PurchaseItem `gorm:"foreignKey:PurchaseID;constraint:OnDelete:CASCADE"`
}
