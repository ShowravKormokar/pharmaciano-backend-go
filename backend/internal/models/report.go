package models

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type Report struct {
	BaseModel

	BranchID      uuid.UUID `gorm:"type:uuid;not null;index"`
	ReportType    string    `gorm:"not null"`
	Period        string
	GeneratedData datatypes.JSON `gorm:"type:jsonb"`
	GeneratedBy   uuid.UUID      `gorm:"type:uuid;not null;index"`

	// Relations
	Branch Branch
	User   User `gorm:"foreignKey:GeneratedBy"`
}
