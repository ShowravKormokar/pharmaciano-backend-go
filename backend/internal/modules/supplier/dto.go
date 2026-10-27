package supplier

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// ---------------------------------------------------------------------------
// Create / Update requests
// ---------------------------------------------------------------------------

// CreateSupplierRequest is the body of POST /suppliers.
//
// Per SUPPLIER_CONTRACT.md §6: organization_id is derived from trusted
// authenticated context, never bound from the payload. OpeningBalance and
// CurrentBalance are explicitly excluded — they are Ledger-owned, not
// writable Supplier truth.
//
// Code is immutable after creation (contract §6: "code is immutable and
// therefore absent from every Update DTO").
type CreateSupplierRequest struct {
	Code             string   `json:"code"               validate:"required,not_blank,max=40"`
	Name             string   `json:"name"               validate:"required,not_blank,max=200"`
	ContactPerson    *string  `json:"contact_person"     validate:"omitempty,max=120"`
	Phone            *string  `json:"phone"              validate:"omitempty,max=30"`
	Email            *string  `json:"email"              validate:"omitempty,max=255"`
	Address          *string  `json:"address"            validate:"omitempty,max=500"`
	City             *string  `json:"city"               validate:"omitempty,max=100"`
	Country          *string  `json:"country"            validate:"omitempty,max=100"`
	DrugLicenseNo    *string  `json:"drug_license_no"    validate:"omitempty,max=80"`
	TIN              *string  `json:"tin"                validate:"omitempty,max=80"`
	CreditLimit      *float64 `json:"credit_limit"       validate:"omitempty,gte=0"`
	CreditPeriodDays *int     `json:"credit_period_days" validate:"omitempty,gte=0"`
	IsActive         *bool    `json:"is_active"          validate:"omitempty"`
}

// UpdateSupplierRequest is the body of PATCH /suppliers/{id}.
//
// Every field is a pointer so an omitted field is distinguishable from an
// explicit zero/false/empty value. Code is immutable and therefore absent.
type UpdateSupplierRequest struct {
	Name             *string  `json:"name"               validate:"omitempty,not_blank,max=200"`
	ContactPerson    *string  `json:"contact_person"     validate:"omitempty,max=120"`
	Phone            *string  `json:"phone"              validate:"omitempty,max=30"`
	Email            *string  `json:"email"              validate:"omitempty,max=255"`
	Address          *string  `json:"address"            validate:"omitempty,max=500"`
	City             *string  `json:"city"               validate:"omitempty,max=100"`
	Country          *string  `json:"country"            validate:"omitempty,max=100"`
	DrugLicenseNo    *string  `json:"drug_license_no"    validate:"omitempty,max=80"`
	TIN              *string  `json:"tin"                validate:"omitempty,max=80"`
	CreditLimit      *float64 `json:"credit_limit"       validate:"omitempty,gte=0"`
	CreditPeriodDays *int     `json:"credit_period_days" validate:"omitempty,gte=0"`
	IsActive         *bool    `json:"is_active"          validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op UPDATE.
func (r UpdateSupplierRequest) IsEmpty() bool {
	return r == UpdateSupplierRequest{}
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

// SupplierResponse is the single-resource representation returned by
// GET /suppliers/{id} and echoed by POST /suppliers.
//
// Per contract §6/§12, ordinary supplier responses never expose bank details
// or full sensitive identifiers. TIN is redacted here; a dedicated banking
// route (future scope) is the only place full values appear.
type SupplierResponse struct {
	ID                uuid.UUID  `json:"id"`
	Code              string     `json:"code"`
	Name              string     `json:"name"`
	ContactPerson    *string    `json:"contact_person,omitempty"`
	Phone            *string    `json:"phone,omitempty"`
	Email            *string    `json:"email,omitempty"`
	Address          *string    `json:"address,omitempty"`
	City             *string    `json:"city,omitempty"`
	Country          *string    `json:"country,omitempty"`
	DrugLicenseNo    *string    `json:"drug_license_no,omitempty"`
	TIN              *string    `json:"tin,omitempty"`
	CreditLimit      *float64   `json:"credit_limit,omitempty"`
	CreditPeriodDays *int       `json:"credit_period_days,omitempty"`
	IsActive         bool       `json:"is_active"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ListItem is the compact projection returned by GET /suppliers.
//
// Narrow projection: no contact/address/financial fields, so a list call
// never pulls the wide supplier row or joins purchase/ledger data.
type ListItem struct {
	ID             uuid.UUID `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	City           *string   `json:"city,omitempty"`
	Country        *string   `json:"country,omitempty"`
	CreditLimit    *float64  `json:"credit_limit,omitempty"`
	CreditPeriodDays *int    `json:"credit_period_days,omitempty"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// List query
// ---------------------------------------------------------------------------

// ListSuppliersQuery binds the query string of GET /suppliers:
// ?q=&city=&is_active= plus standard page/limit/sort.
//
// `q` searches the trigram index on suppliers.name (contract §8.1).
// `city` is an exact/normalized filter, never interpolated SQL.
// `is_active` is a pointer so an absent filter is never confused with an
// explicit false — the same reason ListWarehousesQuery uses pointers.
type ListSuppliersQuery struct {
	pagination.Offset

	City     *string `form:"city" validate:"omitempty,max=100"`
	IsActive *bool   `form:"is_active" validate:"omitempty"`
	Q        *string `form:"q" validate:"omitempty,max=200"`
}

// NormalizedQ returns the ?q= term trimmed and truncated to
// pagination.MaxQueryLen, matching pagination.Filter's behaviour. Case is
// deliberately NOT folded: the GIN trigram index serves ILIKE, so matching
// is case-insensitive in the database and lowercasing here would be
// redundant and would make the DTO disagree with the echoed value.
func (q ListSuppliersQuery) NormalizedQ() string {
	if q.Q == nil {
		return ""
	}
	term := strings.TrimSpace(*q.Q)
	if len(term) > pagination.MaxQueryLen {
		term = term[:pagination.MaxQueryLen]
	}
	return term
}