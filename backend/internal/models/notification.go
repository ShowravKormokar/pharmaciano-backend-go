package models

import (
	"github.com/google/uuid"
)

type Notification struct {
	BaseModel

	UserID   uuid.UUID `gorm:"type:uuid;not null;index"`
	BranchID uuid.UUID `gorm:"type:uuid;not null;index"`
	Type     string    `gorm:"not null"` // low_stock, expiry, approval
	Message  string    `gorm:"not null"`
	IsRead   bool      `gorm:"default:false"`

	// Relations
	User   User   `gorm:"foreignKey:UserID"`
	Branch Branch `gorm:"foreignKey:BranchID"`
}
