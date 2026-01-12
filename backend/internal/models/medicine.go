package models

type Medicine struct {
	BaseModel

	Name        string `gorm:"not null"`
	GenericName string
	MRP         float64 `gorm:"not null"`

	CategoryID *uint
	BrandID    *uint

	IsPrescriptionRequired bool `gorm:"default:false"`

	Category *Category
	Brand    *Brand
}
