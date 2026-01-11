package models

import "time"

type Account struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"unique;not null"`
	Type      string `gorm:"not null"` // asset, liability, income, expense
	CreatedAt time.Time

	// Relations
	DebitEntries  []JournalEntry `gorm:"foreignKey:DebitAccountID"`
	CreditEntries []JournalEntry `gorm:"foreignKey:CreditAccountID"`
}
