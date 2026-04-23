package models

import "github.com/google/uuid"

type Warehouse struct {
	BaseModel

	BranchID uuid.UUID
	Name     string
	Location string
	Capacity int

	// Relations
	Branch    Branch
	Inventory []InventoryBatch
}
