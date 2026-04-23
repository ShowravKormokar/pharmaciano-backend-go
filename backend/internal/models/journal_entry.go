package models

import "github.com/google/uuid"

type JournalEntry struct {
	BaseModel

	BranchID uuid.UUID
	DebitID  uuid.UUID
	CreditID uuid.UUID
	Amount   float64
}
