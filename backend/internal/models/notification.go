package models

import "time"

type Notification struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    uint   `gorm:"index;not null"`
	BranchID  uint   `gorm:"index;not null"`
	Type      string `gorm:"not null"` // low_stock, expiry, approval
	Message   string `gorm:"not null"`
	IsRead    bool   `gorm:"default:false"`
	CreatedAt time.Time

	// Relations
	User   User   `gorm:"foreignKey:UserID"`
	Branch Branch `gorm:"foreignKey:BranchID"`
}
