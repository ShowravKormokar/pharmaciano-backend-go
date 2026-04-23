package models

type Category struct {
	BaseModel

	Name        string `gorm:"unique;not null"`
	Description string
	IsActive    bool `gorm:"default:true"`

	// Relations
	Medicines []Medicine
}
