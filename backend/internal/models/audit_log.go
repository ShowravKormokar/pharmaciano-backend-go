package models

import "github.com/google/uuid"

type AuditLog struct {
	BaseModel

	UserID uuid.UUID `gorm:"type:uuid;index"`

	Action string `gorm:"size:50;not null"`
	Module string `gorm:"size:50;not null"`

	IP string `gorm:"size:45"`

	Browser  string `gorm:"size:100"`
	OS       string `gorm:"size:100"`
	Device   string `gorm:"size:150"`
	Location string `gorm:"size:150"`

	UserAgent string `gorm:"size:255"`

	Details string `gorm:"type:text"`
}
