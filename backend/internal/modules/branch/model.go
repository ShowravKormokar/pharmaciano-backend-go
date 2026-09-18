// Package branch implements the branch module: the physical pharmacy outlets
// (stores) that belong to an organization. A branch is the primary
// branch-scoping unit — users, stock, sales and purchases are all attributed to
// one. Exactly one branch per organization is the "default" (is_default), used
// when a request does not name a branch explicitly.
package branch

import (
	"github.com/google/uuid"

	"backend/internal/common"
)

// Branch is a physical outlet of an organization. Field order and db tags mirror
// migrations/000002 (branches) exactly so the repository can SELECT a fixed
// column list and Scan straight into this struct. Nullable columns are pointers.
//
// OrganizationID is never taken from client input: it is bound from the caller's
// token (appctx.OrgID) on every write, so a branch can only ever be created or
// mutated inside the caller's own tenant.
type Branch struct {
	common.BaseModel

	OrganizationID uuid.UUID `db:"organization_id" json:"organization_id"`

	Code      string `db:"code"       json:"code"` // unique within org (non-deleted)
	Name      string `db:"name"       json:"name"`
	IsActive  bool   `db:"is_active"  json:"is_active"`
	IsDefault bool   `db:"is_default" json:"is_default"`

	Address    *string `db:"address"     json:"address,omitempty"`
	City       *string `db:"city"        json:"city,omitempty"`
	State      *string `db:"state"       json:"state,omitempty"`
	PostalCode *string `db:"postal_code" json:"postal_code,omitempty"`
	Country    *string `db:"country"     json:"country,omitempty"`
	Email      *string `db:"email"       json:"email,omitempty"`
	Phone      *string `db:"phone"       json:"phone,omitempty"`

	Latitude  *float64 `db:"latitude"  json:"latitude,omitempty"`
	Longitude *float64 `db:"longitude" json:"longitude,omitempty"`

	OpenTime  *string `db:"open_time"  json:"open_time,omitempty"`  // HH:MM (24h)
	CloseTime *string `db:"close_time" json:"close_time,omitempty"` // HH:MM (24h)
}
