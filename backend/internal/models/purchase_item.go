package models

type PurchaseItem struct {
	BaseModel

	PurchaseID uint `gorm:"not null;index"`
	MedicineID uint `gorm:"not null;index"`

	BatchNo    string `gorm:"not null"`
	ExpiryDate string
	Quantity   int     `gorm:"not null"`
	UnitCost   float64 `gorm:"not null"`

	Medicine Medicine `gorm:"foreignKey:MedicineID"`
}
