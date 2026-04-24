package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	BaseModel

	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index"`
	BranchID       *uuid.UUID `gorm:"type:uuid;index"`
	RoleID         uuid.UUID  `gorm:"type:uuid;not null;index"`

	Name         string     `gorm:"not null"`
	Email        string     `gorm:"uniqueIndex;not null"`
	Phone        string     `gorm:"size:20"`
	PasswordHash string     `gorm:"not null"`
	Status       string     `gorm:"type:varchar(20);default:'active'"`
	LastLoginAt  *time.Time `gorm:""`
	JoiningDate  time.Time  `gorm:"not null"`

	NID              string
	PresentAddress   string
	PermanentAddress string
	EducationalBG    string

	Organization Organization `gorm:"foreignKey:OrganizationID"`
	Branch       *Branch      `gorm:"foreignKey:BranchID"`
	Role         Role         `gorm:"foreignKey:RoleID"`
}
