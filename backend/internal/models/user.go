package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	BaseModel

	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index"`
	BranchID       *uuid.UUID `gorm:"type:uuid"`
	RoleID         uuid.UUID  `gorm:"type:uuid;not null;index"`

	Name         string     `gorm:"not null" json:"name"`
	Email        string     `gorm:"unique;not null" json:"email"`
	Phone        string     `gorm:"size:20" json:"phone,omitempty"`
	PasswordHash string     `gorm:"not null" json:"-"`
	Status       string     `gorm:"type:varchar(20);default:'active'" json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	JoiningDate  time.Time  `gorm:"not null" json:"joining_date"`

	// Personal info
	NID              string `gorm:"type:varchar(20)" json:"nid"`
	PresentAddress   string `gorm:"type:varchar(255)" json:"present_address"`
	PermanentAddress string `gorm:"type:varchar(255)" json:"permanent_address"`
	EducationalBG    string `gorm:"type:varchar(255)" json:"educational_background"`

	Organization Organization `gorm:"foreignKey:OrganizationID"`
	Branch       *Branch      `gorm:"foreignKey:BranchID"`
	Role         Role         `gorm:"foreignKey:RoleID"`
}
