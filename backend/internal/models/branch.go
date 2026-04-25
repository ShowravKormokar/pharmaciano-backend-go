package models

import "github.com/google/uuid"

type Branch struct {
	BaseModel
	OrganizationID uuid.UUID   `gorm:"type:uuid;not null;index" json:"organization_id"`
	Name           string      `gorm:"not null" json:"name"`
	Address        string      `json:"address,omitempty"`
	Email          string      `json:"email,omitempty"`
	Phone          string      `json:"phone,omitempty"`
	IsActive       bool        `gorm:"default:true" json:"is_active"`

	Organization Organization `gorm:"foreignKey:OrganizationID" json:"-"` // ignore back-reference
}