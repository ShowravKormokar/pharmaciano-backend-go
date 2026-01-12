package models

import "time"

type User struct {
	BaseModel

	OrganizationID uint `gorm:"not null;index"`
	BranchID       *uint
	RoleID         uint `gorm:"not null;index"`

	Name         string     `gorm:"not null" json:"name"`
	Email        string     `gorm:"unique;not null" json:"email"`
	Phone        string     `gorm:"size:20" json:"phone,omitempty"`
	PasswordHash string     `gorm:"not null" json:"-"`
	Status       string     `gorm:"type:varchar(20);default:'active'" json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`

	Organization Organization `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
	Branch       *Branch      `gorm:"foreignKey:BranchID" json:"branch,omitempty"`
	Role         Role         `gorm:"foreignKey:RoleID" json:"role"`
}
