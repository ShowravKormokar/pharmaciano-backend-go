package models

import (
	"time"

	"gorm.io/datatypes"
)

type Report struct {
	ID            uint   `gorm:"primaryKey"`
	BranchID      uint   `gorm:"index;not null"`
	ReportType    string `gorm:"not null"`
	Period        string
	GeneratedData datatypes.JSON `gorm:"type:jsonb"`
	GeneratedBy   uint           `gorm:"index;not null"`
	CreatedAt     time.Time

	// Relations
	Branch Branch `gorm:"foreignKey:BranchID"`
	User   User   `gorm:"foreignKey:GeneratedBy"`
}
