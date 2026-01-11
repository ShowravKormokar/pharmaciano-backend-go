package models

import "time"

type AuditLog struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    uint   `gorm:"index;not null"`
	Action    string `gorm:"not null"`
	Module    string `gorm:"not null"`
	IPAddress string
	CreatedAt time.Time

	// Relations
	User User `gorm:"foreignKey:UserID"`
}
