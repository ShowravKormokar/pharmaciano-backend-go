package models

type SaleItem struct {
	BaseModel

	SaleID     uint `gorm:"not null;index"`
	MedicineID uint `gorm:"not null;index"`

	BatchNo  string
	Quantity int
	Price    float64

	Medicine Medicine
}
