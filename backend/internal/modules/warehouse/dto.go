package warehouse

import (
	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// CreateWarehouseRequest is the body of POST /warehouses. organization_id is
// taken from the caller's token, never the body. branch_id IS required here and
// is validated to belong to the caller's organization before insert. code is
// unique per branch (surfaced as 409 AlreadyExists on conflict).
type CreateWarehouseRequest struct {
	BranchID uuid.UUID `json:"branch_id" validate:"required"`

	Code     string  `json:"code"     validate:"required,min=1,max=40"`
	Name     string  `json:"name"     validate:"required,min=2,max=200"`
	Location *string `json:"location" validate:"omitempty,max=255"`
	Capacity *int    `json:"capacity" validate:"omitempty,gte=0"`
	IsActive *bool   `json:"is_active" validate:"omitempty"`
	IsMain   *bool   `json:"is_main"   validate:"omitempty"`
}

// UpdateWarehouseRequest is the body of PATCH /warehouses/{id}: every field
// optional, nil = leave unchanged. code and branch_id are NOT updatable — a
// warehouse's code is its stable identifier within the branch, and moving a
// warehouse between branches would orphan its stock, so both are fixed at
// creation. Send an empty string to blank the free-text location.
type UpdateWarehouseRequest struct {
	Name     *string `json:"name"     validate:"omitempty,min=2,max=200"`
	Location *string `json:"location" validate:"omitempty,max=255"`
	Capacity *int    `json:"capacity" validate:"omitempty,gte=0"`
	IsActive *bool   `json:"is_active" validate:"omitempty"`
	IsMain   *bool   `json:"is_main"   validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op UPDATE.
func (r UpdateWarehouseRequest) IsEmpty() bool {
	return r == UpdateWarehouseRequest{}
}

// ListWarehousesQuery binds the query string of GET /warehouses:
// ?branch_id=&is_active=&is_main= plus standard page/limit/sort. Pointers make
// "absent" distinguishable from an explicit false/zero value.
type ListWarehousesQuery struct {
	pagination.Offset

	BranchID *uuid.UUID `form:"branch_id"`
	IsActive *bool      `form:"is_active"`
	IsMain   *bool      `form:"is_main"`
}

// ListItem is the compact projection returned by the list endpoints.
type ListItem struct {
	ID       uuid.UUID `json:"id"`
	BranchID uuid.UUID `json:"branch_id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	IsActive bool      `json:"is_active"`
	IsMain   bool      `json:"is_main"`
	Capacity *int      `json:"capacity,omitempty"`
}
