package models

type Account struct {
	BaseModel

	Name string `gorm:"unique;not null"`
	Type string `gorm:"not null"` // asset, liability
}
