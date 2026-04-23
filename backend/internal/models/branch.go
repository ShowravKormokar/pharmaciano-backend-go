package models

import "github.com/google/uuid"

type Branch struct {
	BaseModel

	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index"`
	Name           string    `gorm:"not null"`
	Address        string
	Phone          string
	IsActive       bool `gorm:"default:true"`

	Organization Organization
	Users        []User
	Warehouses   []Warehouse
}
