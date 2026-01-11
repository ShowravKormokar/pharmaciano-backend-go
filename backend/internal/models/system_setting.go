package models

import "time"

type SystemSetting struct {
	ID        uint   `gorm:"primaryKey"`
	Key       string `gorm:"unique;not null"`
	Value     string
	UpdatedAt time.Time
}
