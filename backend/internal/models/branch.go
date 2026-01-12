package models

type Branch struct {
	BaseModel

	OrganizationID uint   `gorm:"not null;index"`
	Name           string `gorm:"not null"`
	Address        string
	Phone          string
	IsActive       bool `gorm:"default:true"`

	Organization Organization
	Users        []User
	Warehouses   []Warehouse
}
