package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	BaseModel
	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"organization_id"`
	BranchID       *uuid.UUID `gorm:"type:uuid;index" json:"branch_id"`
	RoleID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"role_id"`

	Name         string     `gorm:"not null" json:"name"`
	Email        string     `gorm:"uniqueIndex;not null" json:"email"`
	Phone        string     `gorm:"size:20" json:"phone,omitempty"`
	PasswordHash string     `gorm:"not null" json:"-"` // NEVER leak the hash
	Status       string     `gorm:"type:varchar(20);default:'active'" json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	JoiningDate  time.Time  `gorm:"not null" json:"joining_date"`

	NID              string `json:"nid,omitempty"`
	PresentAddress   string `json:"present_address,omitempty"`
	PermanentAddress string `json:"permanent_address,omitempty"`
	EducationalBG    string `json:"educational_background,omitempty"`

	Organization Organization `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
	Branch       *Branch      `gorm:"foreignKey:BranchID" json:"branch,omitempty"`
	Role         Role         `gorm:"foreignKey:RoleID" json:"role,omitempty"`

	FailedAttempts int        `gorm:"default:0" json:"-"`
    LockedUntil    *time.Time `json:"-"`
}
