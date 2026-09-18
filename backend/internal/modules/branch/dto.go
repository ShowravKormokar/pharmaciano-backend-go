package branch

import (
	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// CreateBranchRequest is the body of POST /branches. organization_id is NOT a
// field here: it is always taken from the caller's token so a branch cannot be
// created in another tenant. code is unique per organization (enforced by a
// partial unique index and surfaced as 409 AlreadyExists).
//
// open_time/close_time are validated as 24-hour HH:MM strings (datetime=15:04);
// latitude/longitude are bounded to their valid geographic ranges.
type CreateBranchRequest struct {
	Code      string `json:"code"       validate:"required,min=1,max=40"`
	Name      string `json:"name"       validate:"required,min=2,max=200"`
	IsActive  *bool  `json:"is_active"  validate:"omitempty"`
	IsDefault *bool  `json:"is_default" validate:"omitempty"`

	Address    *string `json:"address"     validate:"omitempty,max=500"`
	City       *string `json:"city"        validate:"omitempty,max=100"`
	State      *string `json:"state"       validate:"omitempty,max=100"`
	PostalCode *string `json:"postal_code" validate:"omitempty,max=20"`
	Country    *string `json:"country"     validate:"omitempty,max=100"`
	Email      *string `json:"email"       validate:"omitempty,email,max=255"`
	Phone      *string `json:"phone"       validate:"omitempty,max=30"`

	Latitude  *float64 `json:"latitude"  validate:"omitempty,gte=-90,lte=90"`
	Longitude *float64 `json:"longitude" validate:"omitempty,gte=-180,lte=180"`

	OpenTime  *string `json:"open_time"  validate:"omitempty,datetime=15:04"`
	CloseTime *string `json:"close_time" validate:"omitempty,datetime=15:04"`
}

// UpdateBranchRequest is the body of PATCH /branches/{id}: every field optional,
// nil = leave unchanged. code is intentionally NOT updatable — a branch's code
// is its stable identifier within the org (referenced by imports, reports and
// external systems); renaming it belongs to a dedicated, audited flow, not the
// general "edit details" endpoint.
//
// As with the organization PATCH, absent and JSON null both decode to nil, so
// this endpoint can set a value but cannot null an optional column; send an
// empty string to blank a free-text field.
type UpdateBranchRequest struct {
	Name      *string `json:"name"       validate:"omitempty,min=2,max=200"`
	IsActive  *bool   `json:"is_active"  validate:"omitempty"`
	IsDefault *bool   `json:"is_default" validate:"omitempty"`

	Address    *string `json:"address"     validate:"omitempty,max=500"`
	City       *string `json:"city"        validate:"omitempty,max=100"`
	State      *string `json:"state"       validate:"omitempty,max=100"`
	PostalCode *string `json:"postal_code" validate:"omitempty,max=20"`
	Country    *string `json:"country"     validate:"omitempty,max=100"`
	Email      *string `json:"email"       validate:"omitempty,email,max=255"`
	Phone      *string `json:"phone"       validate:"omitempty,max=30"`

	Latitude  *float64 `json:"latitude"  validate:"omitempty,gte=-90,lte=90"`
	Longitude *float64 `json:"longitude" validate:"omitempty,gte=-180,lte=180"`

	OpenTime  *string `json:"open_time"  validate:"omitempty,datetime=15:04"`
	CloseTime *string `json:"close_time" validate:"omitempty,datetime=15:04"`
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op UPDATE.
func (r UpdateBranchRequest) IsEmpty() bool {
	return r == UpdateBranchRequest{}
}

// ReplaceBranchRequest is the body of PUT /branches/{id}: a full representation.
// Unlike PATCH, omitted optional fields are set to NULL (a true replace), so the
// resulting row is exactly what the client sent. code is still immutable and so
// is not part of the replaceable surface.
type ReplaceBranchRequest struct {
	Name      string `json:"name"       validate:"required,min=2,max=200"`
	IsActive  bool   `json:"is_active"`
	IsDefault bool   `json:"is_default"`

	Address    *string `json:"address"     validate:"omitempty,max=500"`
	City       *string `json:"city"        validate:"omitempty,max=100"`
	State      *string `json:"state"       validate:"omitempty,max=100"`
	PostalCode *string `json:"postal_code" validate:"omitempty,max=20"`
	Country    *string `json:"country"     validate:"omitempty,max=100"`
	Email      *string `json:"email"       validate:"omitempty,email,max=255"`
	Phone      *string `json:"phone"       validate:"omitempty,max=30"`

	Latitude  *float64 `json:"latitude"  validate:"omitempty,gte=-90,lte=90"`
	Longitude *float64 `json:"longitude" validate:"omitempty,gte=-180,lte=180"`

	OpenTime  *string `json:"open_time"  validate:"omitempty,datetime=15:04"`
	CloseTime *string `json:"close_time" validate:"omitempty,datetime=15:04"`
}

// ListBranchesQuery binds the query string of GET /branches:
// ?is_active=&city=&q=  plus standard page/limit/sort. IsActive is a pointer so
// "absent" (all branches) is distinguishable from "?is_active=false".
// Q is a free-text search matched against code and name.
type ListBranchesQuery struct {
	pagination.Offset

	IsActive *bool  `form:"is_active"`
	City     string `form:"city"`
	Q        string `form:"q"`
}

// ListItem is the compact projection returned by GET /branches — enough for a
// list/table view without the full address/geo payload of a detail read.
type ListItem struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	IsActive  bool      `json:"is_active"`
	IsDefault bool      `json:"is_default"`
	City      *string   `json:"city,omitempty"`
	Phone     *string   `json:"phone,omitempty"`
}
