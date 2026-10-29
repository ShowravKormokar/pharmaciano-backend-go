package customer

import (
	"backend/pkg/pagination"
	"time"

	"github.com/google/uuid"
)

// CreateCustomerRequest is the body of POST /api/v1/customers.
// organization_id, code, loyalty_points, credit_limit, current_due, is_active,
// timestamps and audit fields are server-derived/owned and must never be sent by
// the client.
type CreateCustomerRequest struct {
	// Identity
	Name string `json:"name" validate:"required,min=1,max=160"`

	// Contacts
	Phone *string `json:"phone" validate:"omitempty,max=30"`
	Email *string `json:"email" validate:"omitempty,email,max=255"`

	// Optional profile fields (only collect if justified by approved workflow)
	Address     *string    `json:"address"      validate:"omitempty,max=255"`
	City        *string    `json:"city"         validate:"omitempty,max=100"`
	DateOfBirth *time.Time `json:"date_of_birth"`
	Gender      *string    `json:"gender"       validate:"omitempty,oneof=male female other unknown"`
	BloodGroup  *string    `json:"blood_group"  validate:"omitempty,max=10"`
	Notes       *string    `json:"notes"        validate:"omitempty,max=1000"`

	// Branch visibility for the created customer. At least one branch must be
	// provided and must be within the caller's authorized branch set.
	BranchIDs []uuid.UUID `json:"branch_ids" validate:"required,min=1,dive,required"`
}

// UpdateCustomerRequest is the body of PATCH /api/v1/customers/{id}.
// Every field is optional. Omitted fields are left unchanged. Only allow-listed
// identity/contact fields may be updated here. Financial values (credit_limit,
// current_due), loyalty points, organization ownership, code, branch grants and
// consent history are never client-writable.
type UpdateCustomerRequest struct {
	Name    *string `json:"name"    validate:"omitempty,min=1,max=160"`
	Phone   *string `json:"phone"   validate:"omitempty,max=30"`
	Email   *string `json:"email"   validate:"omitempty,email,max=255"`
	Address *string `json:"address" validate:"omitempty,max=255"`
	City    *string `json:"city"    validate:"omitempty,max=100"`

	DateOfBirth *time.Time `json:"date_of_birth"`
	Gender      *string    `json:"gender"       validate:"omitempty,oneof=male female other unknown"`
	BloodGroup  *string    `json:"blood_group"  validate:"omitempty,max=10"`
	Notes       *string    `json:"notes"        validate:"omitempty,max=1000"`

	// Optimistic concurrency token if the repository exposes one. The service
	// will enforce it against the current record version.
	Version *int64 `json:"version" validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateCustomerRequest) IsEmpty() bool {
	return r.Name == nil && r.Phone == nil && r.Email == nil &&
		r.Address == nil && r.City == nil && r.DateOfBirth == nil &&
		r.Gender == nil && r.BloodGroup == nil && r.Notes == nil &&
		r.Version == nil
}

// ListCustomersQuery binds GET /api/v1/customers query parameters.
// min_due is not evaluated against customers.current_due (ledger-owned). q is
// bounded and searched against allowed name/contact lookup paths only.
type ListCustomersQuery struct {
	pagination.Offset

	Q        string `form:"q"         validate:"omitempty,max=100"`
	IsActive *bool  `form:"is_active"`

	// Branch filter is derived from the caller's authorized branch set server-side.
	// Client-supplied branch filters are not trusted to broaden scope.
	BranchID *uuid.UUID `form:"branch_id"`

	// Not applied by the customer repository. If present, it must be delegated to
	// Ledger with ledger:view authorization.
	MinDue *string `form:"min_due" validate:"omitempty,numeric"`
}

// ListItem is the compact projection returned by GET /api/v1/customers.
type ListItem struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Code           *string   `json:"code,omitempty"`
	Name           string    `json:"name"`
	Phone          *string   `json:"phone,omitempty"`
	Email          *string   `json:"email,omitempty"`
	City           *string   `json:"city,omitempty"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// DetailResponse is the redacted detail projection for GET /api/v1/customers/{id}.
// Financial fields are only returned if sourced via an approved delegated
// contract and the caller has appropriate ledger permissions.
type DetailResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Code           *string    `json:"code,omitempty"`
	Name           string     `json:"name"`
	Phone          *string    `json:"phone,omitempty"`
	Email          *string    `json:"email,omitempty"`
	Address        *string    `json:"address,omitempty"`
	City           *string    `json:"city,omitempty"`
	DateOfBirth    *time.Time `json:"date_of_birth,omitempty"`
	Gender         *string    `json:"gender,omitempty"`
	BloodGroup     *string    `json:"blood_group,omitempty"`
	LoyaltyPoints  int        `json:"loyalty_points"`
	CreditLimit    *float64   `json:"credit_limit,omitempty"`
	CurrentDue     *float64   `json:"current_due,omitempty"`
	Currency       *string    `json:"currency,omitempty"`
	Notes          *string    `json:"notes,omitempty"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

// BranchVisibilityItem represents an explicit branch grant.
type BranchVisibilityItem struct {
	BranchID  uuid.UUID  `json:"branch_id"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
}

// GrantBranchVisibilityRequest is the body for granting branch visibility.
// Organization and branch consistency are enforced server-side.
type GrantBranchVisibilityRequest struct {
	BranchIDs []uuid.UUID `json:"branch_ids" validate:"required,min=1,dive,required"`
}

// RevokeBranchVisibilityRequest is the body for revoking branch visibility.
type RevokeBranchVisibilityRequest struct {
	BranchIDs []uuid.UUID `json:"branch_ids" validate:"required,min=1,dive,required"`
}

// ConsentRequest is the body of POST /api/v1/customers/{id}/consents.
// Consent is append-only; a new record is created for each grant/withdrawal.
type ConsentRequest struct {
	Purpose   string     `json:"purpose"  validate:"required,max=50"`
	Channel   string     `json:"channel"  validate:"required,max=50"`
	Decision  string     `json:"decision" validate:"required,oneof=granted withdrawn"`
	ExpiresAt *time.Time `json:"expires_at"`
	Evidence  *string    `json:"evidence" validate:"omitempty,max=255"`
}

// ConsentItem is a consent evidence record (redacted).
type ConsentItem struct {
	ID         uuid.UUID  `json:"id"`
	Purpose    string     `json:"purpose"`
	Channel    string     `json:"channel"`
	Decision   string     `json:"decision"`
	RecordedAt time.Time  `json:"recorded_at"`
	ActorID    *uuid.UUID `json:"actor_id,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// ExportRequest is the body of POST /api/v1/customers/exports.
// Purpose and bounded filters are required. Source-data permissions may be
// enforced depending on included fields.
type ExportRequest struct {
	Purpose string         `json:"purpose" validate:"required,min=3,max=255"`
	Format  string         `json:"format"  validate:"required,oneof=csv xlsx"`
	Filters map[string]any `json:"filters"`
	Fields  []string       `json:"fields"  validate:"omitempty,dive,max=50"`
}

// ExportJobResponse represents a created export job.
type ExportJobResponse struct {
	JobID     uuid.UUID `json:"job_id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ExportJobStatusResponse represents export job status.
type ExportJobStatusResponse struct {
	JobID       uuid.UUID  `json:"job_id"`
	Status      string     `json:"status"`
	RowCount    *int64     `json:"row_count,omitempty"`
	FileSize    *int64     `json:"file_size,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

// ArchiveRequest is the body of DELETE /api/v1/customers/{id} (archive).
// Reason may be required by policy for audit.
type ArchiveRequest struct {
	Reason *string `json:"reason" validate:"omitempty,max=300"`
}
