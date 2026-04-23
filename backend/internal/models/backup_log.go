package models

import "github.com/google/uuid"

type BackupLog struct {
	BaseModel

	BackupType  string
	Location    string
	PerformedBy uuid.UUID

	// Relations
	User User `gorm:"foreignKey:PerformedBy"`
}
