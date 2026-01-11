package models

import "time"

type Customer struct {
	ID        uint `gorm:"primaryKey"`
	Name      string
	Phone     string
	Email     string
	Address   string
	CreatedAt time.Time

	// Relations
	Sales []Sale
}
