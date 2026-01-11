package models

import "time"

type JournalEntry struct {
	ID              uint    `gorm:"primaryKey"`
	BranchID        uint    `gorm:"index;not null"`
	DebitAccountID  uint    `gorm:"index;not null"`
	CreditAccountID uint    `gorm:"index;not null"`
	Amount          float64 `gorm:"not null"`
	ReferenceType   string
	ReferenceID     uint
	CreatedAt       time.Time

	// Relations
	Branch        Branch  `gorm:"foreignKey:BranchID"`
	DebitAccount  Account `gorm:"foreignKey:DebitAccountID"`
	CreditAccount Account `gorm:"foreignKey:CreditAccountID"`
}
