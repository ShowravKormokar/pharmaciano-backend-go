// Package warehouse implements the warehouse module: storage locations that
// belong to a branch (which in turn belongs to an organization). Stock is held
// against a warehouse, and exactly one warehouse per branch may be the "main"
// one (is_main), used as the default stock location for that branch.
package warehouse

import (
	"github.com/google/uuid"

	"backend/internal/common"
)

// Warehouse is a storage location under a branch. Field order and db tags mirror
// migrations/000002 (warehouses) exactly so the repository can SELECT a fixed
// column list and Scan straight into this struct. Nullable columns are pointers.
//
// Both OrganizationID and BranchID are NOT NULL and are never taken from client
// input: organization_id is bound from the caller's token and branch_id is
// validated to belong to that organization before insert, so a warehouse can
// only ever be created under a branch the caller actually owns.
type Warehouse struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`
	BranchID       uuid.UUID `db:"branch_id"       json:"branch_id"`

	Code     string  `db:"code"      json:"code"` // unique within branch (non-deleted)
	Name     string  `db:"name"      json:"name"`
	Location *string `db:"location"  json:"location,omitempty"`
	Capacity *int    `db:"capacity"  json:"capacity,omitempty"` // in units
	IsActive bool    `db:"is_active" json:"is_active"`
	IsMain   bool    `db:"is_main"   json:"is_main"`
}
