package models

import "time"

type BackupLog struct {
	ID          uint   `gorm:"primaryKey"`
	BackupType  string `gorm:"not null"`
	Location    string
	PerformedBy uint `gorm:"index;not null"`
	CreatedAt   time.Time

	// Relations
	User User `gorm:"foreignKey:PerformedBy"`
}
