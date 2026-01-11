package models

type Brand struct {
	ID           uint   `gorm:"primaryKey"`
	Name         string `gorm:"unique;not null"`
	Manufacturer string
	Country      string

	// Relations
	Medicines []Medicine
}
