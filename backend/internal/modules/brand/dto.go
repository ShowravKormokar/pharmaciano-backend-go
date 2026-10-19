package brand

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// Schema limits transcribed from migrations/000005_master_data.up.sql
const (
	maxCodeLen    = 40
	maxNameLen    = 200
	maxCountryLen = 100
	maxSearchLen  = pagination.MaxQueryLen
)

// CreateBrandRequest is the body of POST /brands.
// organization_id is taken from trusted application context; never accepted here.
// manufacturer_id is required and must belong to the same organization.
type CreateBrandRequest struct {
	Code            string    `json:"code"             validate:"required,not_blank,max=40"`
	Name            string    `json:"name"             validate:"required,not_blank,max=200"`
	ManufacturerID  uuid.UUID `json:"manufacturer_id"  validate:"required"`
	CountryOfOrigin *string   `json:"country_of_origin" validate:"omitempty,max=100"`
	Description     *string   `json:"description"     validate:"omitempty"`
	IsActive        *bool     `json:"is_active"       validate:"omitempty"`
}

// UpdateBrandRequest is the body of PATCH /brands/{id}.
// Every field is optional; nil = leave unchanged. code and organization_id are
// immutable and not updatable.
type UpdateBrandRequest struct {
	Name            *string                `json:"name"             validate:"omitempty,not_blank,max=200"`
	ManufacturerID  *uuid.UUID             `json:"manufacturer_id"  validate:"omitempty"`
	CountryOfOrigin OptionalNullableString `json:"country_of_origin"`
	Description     OptionalNullableString `json:"description"`
	IsActive        *bool                  `json:"is_active"       validate:"omitempty"`
}

// OptionalNullableString distinguishes an omitted PATCH field from explicit
// null, which clears a nullable database column.
type OptionalNullableString struct {
	Set   bool
	Value *string
}

func (v *OptionalNullableString) UnmarshalJSON(data []byte) error {
	v.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		v.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	v.Value = &value
	return nil
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op UPDATE.
func (r UpdateBrandRequest) IsEmpty() bool {
	return r == UpdateBrandRequest{}
}

// ListBrandsQuery binds the query string of GET /brands:
// ?manufacturer_id=&q=&is_active= plus standard page/limit/sort.
// Pointers make "absent" distinguishable from an explicit false value.
type ListBrandsQuery struct {
	pagination.Offset

	ManufacturerID *uuid.UUID `form:"manufacturer_id"`
	Q              string     `form:"q" validate:"omitempty,max=200"`
	IsActive       *bool      `form:"is_active"`
}

// BrandResponse is the single-resource representation returned to clients.
type BrandResponse struct {
	ID              uuid.UUID `json:"id"`
	Code            string    `json:"code"`
	Name            string    `json:"name"`
	ManufacturerID  uuid.UUID `json:"manufacturer_id"`
	CountryOfOrigin *string   `json:"country_of_origin,omitempty"`
	Description     *string   `json:"description,omitempty"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
