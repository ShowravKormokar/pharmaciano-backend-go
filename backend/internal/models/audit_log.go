package models

import "github.com/google/uuid"

type AuditLog struct {
	BaseModel
	UserID    uuid.UUID `gorm:"type:uuid;index"`
	Action    string    `gorm:"size:50;not null"` // LOGIN, LOGOUT, UPDATE, etc.
	Module    string    `gorm:"size:50;not null"`
	IP        string    `gorm:"size:45"`
	UserAgent string    `gorm:"size:255"`
	Details   string    `gorm:"type:text"` // JSON
}
