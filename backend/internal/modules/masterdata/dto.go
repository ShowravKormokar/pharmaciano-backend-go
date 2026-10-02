// Transport DTOs for the eleven central MasterData resources.
//
// Conventions are inherited verbatim from the completed modules (warehouse is the
// reference implementation, contract §9/§11):
//
//   - `json` + `validate` tags only. No `db` tags and no persistence types leak
//     in here; a DTO is an HTTP contract, the model is a database row.
//   - Every PATCH field is a POINTER so an omitted field is distinguishable from
//     an explicit zero/false/empty value (warehouse/dto.go:29-35).
//   - `code` is immutable and therefore absent from every Update DTO, matching
//     warehouse. Renaming a master-data code would silently orphan every row that
//     references it by code in seed files and imports.
//   - Response DTOs are separate types rather than the model itself, so a future
//     column cannot silently become part of the public API.
//
// Lengths below are transcribed from migrations/000005_master_data.up.sql and are
// asserted against the constants in validation.go by
// TestSchemaLimitConstantsMatchMigration (repository_schema_test.go), which parses
// the schema's VARCHAR widths rather than restating them, so a schema change cannot
// silently drift away from the tags.

package masterdata

import (
	"strings"
	"time"

	"github.com/google/uuid"

	uuidx "backend/internal/platform/uuid"
	"backend/pkg/pagination"
)

// ---------------------------------------------------------------------------
// ProductCategory — product_categories
// ---------------------------------------------------------------------------

// CreateProductCategoryRequest is the body of POST /master/product-categories.
//
// ParentID is optional; omitting it creates a root category. SortOrder and
// IsActive are pointers so that omitting them falls through to the schema
// defaults (0 and TRUE respectively) instead of forcing every caller to send
// them.
type CreateProductCategoryRequest struct {
	Code        string     `json:"code"        validate:"required,not_blank,max=40"`
	Name        string     `json:"name"        validate:"required,not_blank,max=160"`
	ParentID    *uuid.UUID `json:"parent_id"   validate:"omitempty"`
	Description *string    `json:"description" validate:"omitempty"`
	SortOrder   *int       `json:"sort_order"  validate:"omitempty,gte=0"`
	IsActive    *bool      `json:"is_active"   validate:"omitempty"`
}

// UpdateProductCategoryRequest is the body of PATCH /master/product-categories/{id}.
type UpdateProductCategoryRequest struct {
	Name        *string    `json:"name"        validate:"omitempty,not_blank,max=160"`
	ParentID    *uuid.UUID `json:"parent_id"   validate:"omitempty"`
	Description *string    `json:"description" validate:"omitempty"`
	SortOrder   *int       `json:"sort_order"  validate:"omitempty,gte=0"`
	IsActive    *bool      `json:"is_active"   validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateProductCategoryRequest) IsEmpty() bool { return r == UpdateProductCategoryRequest{} }

// ProductCategoryResponse is the single-resource representation.
type ProductCategoryResponse struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	Description *string    `json:"description,omitempty"`
	SortOrder   int        `json:"sort_order"`
	IsActive    bool       `json:"is_active"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Route — routes
// ---------------------------------------------------------------------------

// CreateRouteRequest is the body of POST /master/routes.
type CreateRouteRequest struct {
	Code        string  `json:"code"        validate:"required,not_blank,max=40"`
	Name        string  `json:"name"        validate:"required,not_blank,max=120"`
	Description *string `json:"description" validate:"omitempty"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
}

// UpdateRouteRequest is the body of PATCH /master/routes/{id}.
type UpdateRouteRequest struct {
	Name        *string `json:"name"        validate:"omitempty,not_blank,max=120"`
	Description *string `json:"description" validate:"omitempty"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateRouteRequest) IsEmpty() bool { return r == UpdateRouteRequest{} }

// RouteResponse is the single-resource representation.
type RouteResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// DosageForm — dosage_forms
// ---------------------------------------------------------------------------

// CreateDosageFormRequest is the body of POST /master/dosage-forms.
// RouteID is optional at the transport level; whether the route exists is a
// service-layer concern (contract §12).
type CreateDosageFormRequest struct {
	Code        string     `json:"code"        validate:"required,not_blank,max=40"`
	Name        string     `json:"name"        validate:"required,not_blank,max=120"`
	RouteID     *uuid.UUID `json:"route_id"    validate:"omitempty"`
	Description *string    `json:"description" validate:"omitempty"`
	IsActive    *bool      `json:"is_active"   validate:"omitempty"`
}

// UpdateDosageFormRequest is the body of PATCH /master/dosage-forms/{id}.
type UpdateDosageFormRequest struct {
	Name        *string    `json:"name"        validate:"omitempty,not_blank,max=120"`
	RouteID     *uuid.UUID `json:"route_id"    validate:"omitempty"`
	Description *string    `json:"description" validate:"omitempty"`
	IsActive    *bool      `json:"is_active"   validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateDosageFormRequest) IsEmpty() bool { return r == UpdateDosageFormRequest{} }

// DosageFormResponse is the single-resource representation.
type DosageFormResponse struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	RouteID     *uuid.UUID `json:"route_id,omitempty"`
	Description *string    `json:"description,omitempty"`
	IsActive    bool       `json:"is_active"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// PackageType — package_types
// ---------------------------------------------------------------------------

// CreatePackageTypeRequest is the body of POST /master/package-types.
type CreatePackageTypeRequest struct {
	Code        string  `json:"code"        validate:"required,not_blank,max=40"`
	Name        string  `json:"name"        validate:"required,not_blank,max=120"`
	Description *string `json:"description" validate:"omitempty"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
}

// UpdatePackageTypeRequest is the body of PATCH /master/package-types/{id}.
type UpdatePackageTypeRequest struct {
	Name        *string `json:"name"        validate:"omitempty,not_blank,max=120"`
	Description *string `json:"description" validate:"omitempty"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdatePackageTypeRequest) IsEmpty() bool { return r == UpdatePackageTypeRequest{} }

// PackageTypeResponse is the single-resource representation.
type PackageTypeResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// UnitType — unit_types
// ---------------------------------------------------------------------------

// CreateUnitTypeRequest is the body of POST /master/unit-types.
//
// ConversionFactor has NO positive-value rule: contract §11.2 keeps its bounds
// UNRESOLVED and Phase 02 deliberately left the column unconstrained, so adding
// `gt=0` here would invent a business rule. Only representability is enforced, by
// FitsConversionFactor against the NUMERIC(15,6) width the schema allows.
type CreateUnitTypeRequest struct {
	Code             string     `json:"code"              validate:"required,not_blank,max=40"`
	Name             string     `json:"name"              validate:"required,not_blank,max=120"`
	Symbol           *string    `json:"symbol"            validate:"omitempty,max=20"`
	BaseUnitID       *uuid.UUID `json:"base_unit_id"      validate:"omitempty"`
	ConversionFactor *float64   `json:"conversion_factor" validate:"omitempty"`
	IsActive         *bool      `json:"is_active"         validate:"omitempty"`
}

// UpdateUnitTypeRequest is the body of PATCH /master/unit-types/{id}.
type UpdateUnitTypeRequest struct {
	Name             *string    `json:"name"              validate:"omitempty,not_blank,max=120"`
	Symbol           *string    `json:"symbol"            validate:"omitempty,max=20"`
	BaseUnitID       *uuid.UUID `json:"base_unit_id"      validate:"omitempty"`
	ConversionFactor *float64   `json:"conversion_factor" validate:"omitempty"`
	IsActive         *bool      `json:"is_active"         validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateUnitTypeRequest) IsEmpty() bool { return r == UpdateUnitTypeRequest{} }

// UnitTypeResponse is the single-resource representation.
type UnitTypeResponse struct {
	ID               uuid.UUID  `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Symbol           *string    `json:"symbol,omitempty"`
	BaseUnitID       *uuid.UUID `json:"base_unit_id,omitempty"`
	ConversionFactor *float64   `json:"conversion_factor,omitempty"`
	IsActive         bool       `json:"is_active"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// StorageCondition — storage_conditions
// ---------------------------------------------------------------------------

// CreateStorageConditionRequest is the body of POST /master/storage-conditions.
//
// Either temperature bound may be omitted. When both are supplied, min<=max is
// enforced by a struct-level rule registered in validation.go — the same rule
// the database now enforces via ck_storage_conditions_temp_range.
type CreateStorageConditionRequest struct {
	Code           string   `json:"code"             validate:"required,not_blank,max=40"`
	Name           string   `json:"name"             validate:"required,not_blank,max=120"`
	MinTempCelsius *float64 `json:"min_temp_celsius" validate:"omitempty"`
	MaxTempCelsius *float64 `json:"max_temp_celsius" validate:"omitempty"`
	Description    *string  `json:"description"      validate:"omitempty"`
	IsActive       *bool    `json:"is_active"        validate:"omitempty"`
}

// UpdateStorageConditionRequest is the body of PATCH /master/storage-conditions/{id}.
//
// A PATCH that sets only one bound CANNOT be checked here: whether the result is
// still ordered depends on the stored value of the other bound. The service
// layer must load the current row and validate the merged result; the struct
// rule below only rejects a pair that is already inverted on its own.
type UpdateStorageConditionRequest struct {
	Name           *string  `json:"name"             validate:"omitempty,not_blank,max=120"`
	MinTempCelsius *float64 `json:"min_temp_celsius" validate:"omitempty"`
	MaxTempCelsius *float64 `json:"max_temp_celsius" validate:"omitempty"`
	Description    *string  `json:"description"      validate:"omitempty"`
	IsActive       *bool    `json:"is_active"        validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateStorageConditionRequest) IsEmpty() bool { return r == UpdateStorageConditionRequest{} }

// StorageConditionResponse is the single-resource representation.
type StorageConditionResponse struct {
	ID             uuid.UUID `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	MinTempCelsius *float64  `json:"min_temp_celsius,omitempty"`
	MaxTempCelsius *float64  `json:"max_temp_celsius,omitempty"`
	Description    *string   `json:"description,omitempty"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// TaxRate — tax_rates
// ---------------------------------------------------------------------------

// CreateTaxRateRequest is the body of POST /master/tax-rates.
//
// RatePercent is a POINTER, not a float64, even though the column is NOT NULL.
// A plain float64 cannot express "required" without also rejecting 0 — `required`
// tests against the zero value — and 0% is a legal rate (an exempt or
// zero-rated line). A non-nil pointer to 0 is required and accepted; an absent
// field is rejected.
//
// The value must be non-negative and fit NUMERIC(6,3). The upper BUSINESS
// maximum is not a constant: contract §11.2 leaves it UNRESOLVED and no config
// key defines it, so it is supplied by Limits.MaxRatePercent at
// validation-registration time. See RegisterValidations.
type CreateTaxRateRequest struct {
	Code        string   `json:"code"         validate:"required,not_blank,max=40"`
	Name        string   `json:"name"         validate:"required,not_blank,max=120"`
	RatePercent *float64 `json:"rate_percent" validate:"required,gte=0"`
	IsDefault   *bool    `json:"is_default"   validate:"omitempty"`
	IsActive    *bool    `json:"is_active"    validate:"omitempty"`
}

// UpdateTaxRateRequest is the body of PATCH /master/tax-rates/{id}.
type UpdateTaxRateRequest struct {
	Name        *string  `json:"name"         validate:"omitempty,not_blank,max=120"`
	RatePercent *float64 `json:"rate_percent" validate:"omitempty,gte=0"`
	IsDefault   *bool    `json:"is_default"   validate:"omitempty"`
	IsActive    *bool    `json:"is_active"    validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateTaxRateRequest) IsEmpty() bool { return r == UpdateTaxRateRequest{} }

// TaxRateResponse is the single-resource representation.
type TaxRateResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	RatePercent float64   `json:"rate_percent"`
	IsDefault   bool      `json:"is_default"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// DrugGroup — drug_groups
// ---------------------------------------------------------------------------

// CreateDrugGroupRequest is the body of POST /master/drug-groups.
type CreateDrugGroupRequest struct {
	Code        string  `json:"code"        validate:"required,not_blank,max=40"`
	Name        string  `json:"name"        validate:"required,not_blank,max=160"`
	Description *string `json:"description" validate:"omitempty"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
}

// UpdateDrugGroupRequest is the body of PATCH /master/drug-groups/{id}.
type UpdateDrugGroupRequest struct {
	Name        *string `json:"name"        validate:"omitempty,not_blank,max=160"`
	Description *string `json:"description" validate:"omitempty"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateDrugGroupRequest) IsEmpty() bool { return r == UpdateDrugGroupRequest{} }

// DrugGroupResponse is the single-resource representation.
type DrugGroupResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// DrugClass — drug_classes
// ---------------------------------------------------------------------------

// CreateDrugClassRequest is the body of POST /master/drug-classes.
// DrugGroupID is a syntax-checked UUID only; existence is service-layer.
type CreateDrugClassRequest struct {
	Code        string     `json:"code"          validate:"required,not_blank,max=40"`
	Name        string     `json:"name"          validate:"required,not_blank,max=160"`
	DrugGroupID *uuid.UUID `json:"drug_group_id" validate:"omitempty"`
	Description *string    `json:"description"   validate:"omitempty"`
	IsActive    *bool      `json:"is_active"     validate:"omitempty"`
}

// UpdateDrugClassRequest is the body of PATCH /master/drug-classes/{id}.
type UpdateDrugClassRequest struct {
	Name        *string    `json:"name"          validate:"omitempty,not_blank,max=160"`
	DrugGroupID *uuid.UUID `json:"drug_group_id" validate:"omitempty"`
	Description *string    `json:"description"   validate:"omitempty"`
	IsActive    *bool      `json:"is_active"     validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateDrugClassRequest) IsEmpty() bool { return r == UpdateDrugClassRequest{} }

// DrugClassResponse is the single-resource representation.
type DrugClassResponse struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	DrugGroupID *uuid.UUID `json:"drug_group_id,omitempty"`
	Description *string    `json:"description,omitempty"`
	IsActive    bool       `json:"is_active"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// TherapeuticClass — therapeutic_classes
// ---------------------------------------------------------------------------

// CreateTherapeuticClassRequest is the body of POST /master/therapeutic-classes.
// DrugClassID is a syntax-checked UUID only; existence is service-layer.
type CreateTherapeuticClassRequest struct {
	Code        string     `json:"code"          validate:"required,not_blank,max=40"`
	Name        string     `json:"name"          validate:"required,not_blank,max=160"`
	DrugClassID *uuid.UUID `json:"drug_class_id" validate:"omitempty"`
	Description *string    `json:"description"   validate:"omitempty"`
	IsActive    *bool      `json:"is_active"     validate:"omitempty"`
}

// UpdateTherapeuticClassRequest is the body of PATCH /master/therapeutic-classes/{id}.
type UpdateTherapeuticClassRequest struct {
	Name        *string    `json:"name"          validate:"omitempty,not_blank,max=160"`
	DrugClassID *uuid.UUID `json:"drug_class_id" validate:"omitempty"`
	Description *string    `json:"description"   validate:"omitempty"`
	IsActive    *bool      `json:"is_active"     validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateTherapeuticClassRequest) IsEmpty() bool { return r == UpdateTherapeuticClassRequest{} }

// TherapeuticClassResponse is the single-resource representation.
type TherapeuticClassResponse struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	DrugClassID *uuid.UUID `json:"drug_class_id,omitempty"`
	Description *string    `json:"description,omitempty"`
	IsActive    bool       `json:"is_active"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// GenericMedicine — generic_medicines
// ---------------------------------------------------------------------------

// CreateGenericMedicineRequest is the body of POST /master/generic-medicines.
// TherapeuticClassID is a syntax-checked UUID only; existence is service-layer.
type CreateGenericMedicineRequest struct {
	Code               string     `json:"code"                  validate:"required,not_blank,max=40"`
	Name               string     `json:"name"                  validate:"required,not_blank,max=200"`
	Description        *string    `json:"description"           validate:"omitempty"`
	TherapeuticClassID *uuid.UUID `json:"therapeutic_class_id" validate:"omitempty"`
	IsControlled       *bool      `json:"is_controlled"         validate:"omitempty"`
	IsActive           *bool      `json:"is_active"             validate:"omitempty"`
}

// UpdateGenericMedicineRequest is the body of PATCH /master/generic-medicines/{id}.
type UpdateGenericMedicineRequest struct {
	Name               *string    `json:"name"                  validate:"omitempty,not_blank,max=200"`
	Description        *string    `json:"description"           validate:"omitempty"`
	TherapeuticClassID *uuid.UUID `json:"therapeutic_class_id" validate:"omitempty"`
	IsControlled       *bool      `json:"is_controlled"         validate:"omitempty"`
	IsActive           *bool      `json:"is_active"             validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateGenericMedicineRequest) IsEmpty() bool { return r == UpdateGenericMedicineRequest{} }

// GenericMedicineResponse is the single-resource representation.
type GenericMedicineResponse struct {
	ID                 uuid.UUID  `json:"id"`
	Code               string     `json:"code"`
	Name               string     `json:"name"`
	Description        *string    `json:"description,omitempty"`
	TherapeuticClassID *uuid.UUID `json:"therapeutic_class_id,omitempty"`
	IsControlled       bool       `json:"is_controlled"`
	IsActive           bool       `json:"is_active"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// List query
// ---------------------------------------------------------------------------

// ListMasterDataQuery binds the query string of every
// GET /master/{resource}?page=&limit=&sort=&q=&is_active=&parent_id=.
//
// pagination.Offset supplies page/limit/sort with the project's own names,
// defaults (page 1, limit 20) and hard cap (100). IsActive and Q are pointers so
// that an absent filter is never confused with an explicit false/zero — the same
// reason ListWarehousesQuery uses them.
//
// ParentID is a STRING, not a *uuid.UUID, and this is deliberate. gin's form
// binder has no case for [16]byte (which is what uuid.UUID is): it falls through
// to its default and fails with "is not valid value for uuid.UUID". A
// *uuid.UUID `form:` tag therefore cannot bind at all. Parsing the raw value via
// uuidx.ParsePointer — exactly what pkg/pagination/filter.go:60-65 does for
// ?branch_id= — keeps the filter working and gives one deterministic place for
// the "malformed UUID" verdict. Call ParentUUID() at the top of the handler and
// turn a non-nil error into errs.Validation.
//
// NOTE: ListWarehousesQuery declares `BranchID *uuid.UUID form:"branch_id"`, so
// warehouse's own ?branch_id= filter is affected by this same gin limitation.
// That is a pre-existing bug in a completed module, reported rather than fixed
// here.
//
// parent_id applies to the three self-referencing hierarchies
// (product_categories, drug_classes, therapeutic_classes). For the other eight
// resources the field is accepted by the shared query type and ignored by the
// repository, so one query type serves all eleven endpoints.
type ListMasterDataQuery struct {
	pagination.Offset

	IsActive *bool   `form:"is_active" validate:"omitempty"`
	Q        *string `form:"q"         validate:"omitempty,max=200"`
	ParentID string  `form:"parent_id" validate:"omitempty,max=36"`
}

// ParentUUID parses the raw ?parent_id= value. An absent or empty value yields
// (nil, nil) — "no filter", not an error — matching uuidx.ParsePointer.
func (q ListMasterDataQuery) ParentUUID() (*uuid.UUID, error) {
	return uuidx.ParsePointer(q.ParentID)
}

// NormalizedQ returns the ?q= term in the representation Phase 04 must consume:
// trimmed of surrounding whitespace and truncated to pagination.MaxQueryLen —
// exactly what pagination.Filter already does (pkg/pagination/filter.go:50-53).
//
// Case is deliberately NOT folded. Phase 02 indexes `.name` with a GIN trigram
// operator, which serves ILIKE, so matching is case-insensitive in the database
// and lowercasing here would be redundant and would make the DTO disagree with
// the value echoed back in responses.
func (q ListMasterDataQuery) NormalizedQ() string {
	if q.Q == nil {
		return ""
	}
	term := strings.TrimSpace(*q.Q)
	if len(term) > pagination.MaxQueryLen {
		term = term[:pagination.MaxQueryLen]
	}
	return term
}
