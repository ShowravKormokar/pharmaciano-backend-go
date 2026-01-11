package models

import "time"

type User struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"index;not null"`
	BranchID       uint   `gorm:"index"`
	RoleID         uint   `gorm:"index;not null"`
	Name           string `gorm:"not null"`
	Email          string `gorm:"unique;not null"`
	Phone          string
	PasswordHash   string `gorm:"not null"`
	Status         string `gorm:"default:'active'"`
	LastLogin      *time.Time
	CreatedAt      time.Time

	// Relations
	Organization  Organization `gorm:"foreignKey:OrganizationID"`
	Branch        Branch       `gorm:"foreignKey:BranchID"`
	Role          Role         `gorm:"foreignKey:RoleID"`
	Sales         []Sale       `gorm:"foreignKey:CashierID"`
	Notifications []Notification
}
