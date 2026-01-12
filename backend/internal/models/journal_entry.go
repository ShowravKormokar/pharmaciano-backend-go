package models

type JournalEntry struct {
	BaseModel

	BranchID uint
	DebitID  uint
	CreditID uint
	Amount   float64
}
