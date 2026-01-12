package models

import "time"

type User struct {
	BaseModel

	OrganizationID uint `gorm:"not null;index"`
	BranchID       *uint
	RoleID         uint `gorm:"not null;index"`

	Name         string `gorm:"not null"`
	Email        string `gorm:"unique;not null"`
	Phone        string `gorm:"size:20"`
	PasswordHash string `gorm:"not null"`
	Status       string `gorm:"type:varchar(20);default:'active'"`
	LastLoginAt  *time.Time

	Organization Organization `gorm:"foreignKey:OrganizationID"`
	Branch       *Branch      `gorm:"foreignKey:BranchID"`
	Role         Role         `gorm:"foreignKey:RoleID"`
}
