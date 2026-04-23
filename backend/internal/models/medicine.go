package models

import "github.com/google/uuid"

type Medicine struct {
	BaseModel

	Name        string `gorm:"not null"`
	GenericName string
	MRP         float64 `gorm:"not null"`

	CategoryID *uuid.UUID
	BrandID    *uuid.UUID

	IsPrescriptionRequired bool `gorm:"default:false"`

	Category *Category
	Brand    *Brand
}
