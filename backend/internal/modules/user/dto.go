package user

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common/enums"
	"backend/pkg/pagination"
)

// dateLayout is the wire format for the DATE columns (joining_date,
// date_of_birth). Clients send/receive plain calendar dates ("2006-01-02"); the
// service parses them into time.Time. Using a string in the DTO (validated with
// the datetime rule) keeps a malformed date a clean 400 at the edge instead of a
// decode panic, and avoids surprising callers with a full RFC3339 timestamp on a
// field that is conceptually date-only.
const dateLayout = "2006-01-02"

// CreateUserRequest is the body of POST /users. organization_id is never a field
// here — it is bound from the caller's token so a user cannot be created in
// another tenant. The initial password is accepted here, Argon2id-hashed by the
// service, and never stored or echoed in plaintext.
//
// Enum-typed fields (status, stage, employment_type, profile.gender) reject
// unknown values at JSON-decode time via the enums package's UnmarshalJSON, so
// an invalid value is a 400 before the request ever reaches the service.
type CreateUserRequest struct {
	Email    string  `json:"email"    validate:"required,email,max=255"`
	Username *string `json:"username" validate:"omitempty,min=3,max=50"`
	Phone    *string `json:"phone"    validate:"omitempty,max=30"`
	Password string  `json:"password" validate:"required,min=8,max=128"`

	BranchID     *uuid.UUID `json:"branch_id"     validate:"omitempty"`
	EmployeeCode *string    `json:"employee_code" validate:"omitempty,max=40"`

	// Optional lifecycle overrides. Default to active/unverified when omitted so
	// the common case (onboard an active employee who must still verify) needs no
	// input. Creating a user directly in a non-active status is a valid admin act.
	Status *enums.UserStatus `json:"status" validate:"omitempty"`
	Stage  *enums.UserStage  `json:"stage"  validate:"omitempty"`

	// MustChangePassword forces a password change on first login; defaults true so
	// an admin-set initial password is always rotated by the user. Send false to
	// opt out (e.g. self-service signup flows that set their own password).
	MustChangePassword *bool `json:"must_change_password" validate:"omitempty"`

	JoiningDate    *string               `json:"joining_date"    validate:"omitempty,datetime=2006-01-02"`
	EmploymentType *enums.EmploymentType `json:"employment_type" validate:"omitempty"`

	// Profile carries the required human-identity fields. It is validated as a
	// nested struct (validator descends automatically), so omitting it fails on
	// the required first_name/last_name below — a user always has a name.
	Profile CreateProfileInput `json:"profile" validate:"required"`
}

// CreateProfileInput is the identity block of CreateUserRequest.
type CreateProfileInput struct {
	FirstName   string        `json:"first_name"    validate:"required,min=1,max=80"`
	LastName    string        `json:"last_name"     validate:"required,min=1,max=80"`
	MiddleName  *string       `json:"middle_name"   validate:"omitempty,max=80"`
	DisplayName *string       `json:"display_name"  validate:"omitempty,max=160"`
	DateOfBirth *string       `json:"date_of_birth" validate:"omitempty,datetime=2006-01-02"`
	Gender      *enums.Gender `json:"gender"        validate:"omitempty"`
	AvatarURL   *string       `json:"avatar_url"    validate:"omitempty,url,max=500"`
}

// UpdateUserRequest is the body of PATCH /users/{id}: every field optional, nil =
// leave unchanged. It merges editable user columns and editable profile-identity
// columns into one request so the client can update a person in a single call.
//
// Deliberately absent: email (login identity — changed via a dedicated, verified
// flow), password (owned by auth), and status (its own endpoint, because a status
// change has side effects such as revoking active sessions). As with the other
// PATCH endpoints, absent and JSON null both decode to nil, so this endpoint can
// set an optional field but cannot null it; send an empty string to blank a
// free-text field.
type UpdateUserRequest struct {
	Username     *string    `json:"username"      validate:"omitempty,min=3,max=50"`
	Phone        *string    `json:"phone"         validate:"omitempty,max=30"`
	BranchID     *uuid.UUID `json:"branch_id"     validate:"omitempty"`
	EmployeeCode *string    `json:"employee_code" validate:"omitempty,max=40"`

	JoiningDate    *string               `json:"joining_date"    validate:"omitempty,datetime=2006-01-02"`
	EmploymentType *enums.EmploymentType `json:"employment_type" validate:"omitempty"`

	FirstName   *string       `json:"first_name"    validate:"omitempty,min=1,max=80"`
	LastName    *string       `json:"last_name"     validate:"omitempty,min=1,max=80"`
	MiddleName  *string       `json:"middle_name"   validate:"omitempty,max=80"`
	DisplayName *string       `json:"display_name"  validate:"omitempty,max=160"`
	DateOfBirth *string       `json:"date_of_birth" validate:"omitempty,datetime=2006-01-02"`
	Gender      *enums.Gender `json:"gender"        validate:"omitempty"`
	AvatarURL   *string       `json:"avatar_url"    validate:"omitempty,url,max=500"`
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op write. All fields are
// pointers or comparable enum pointers, so the struct is comparable.
func (r UpdateUserRequest) IsEmpty() bool { return r == UpdateUserRequest{} }

// touchesProfile reports whether the PATCH changes any profile-identity field,
// so the service can skip the profile UPDATE when only user columns changed.
func (r UpdateUserRequest) touchesProfile() bool {
	return r.FirstName != nil || r.LastName != nil || r.MiddleName != nil ||
		r.DisplayName != nil || r.DateOfBirth != nil || r.Gender != nil ||
		r.AvatarURL != nil
}

// touchesUser reports whether the PATCH changes any users-table column.
func (r UpdateUserRequest) touchesUser() bool {
	return r.Username != nil || r.Phone != nil || r.BranchID != nil ||
		r.EmployeeCode != nil || r.JoiningDate != nil || r.EmploymentType != nil
}

// ChangeStatusRequest is the body of PATCH /users/{id}/status. Status is
// validated as a known enum at decode time; the service additionally enforces
// legal transitions and drives the side effects (e.g. revoking sessions when a
// user is moved out of an active status). Reason is an optional audit note.
type ChangeStatusRequest struct {
	Status enums.UserStatus `json:"status" validate:"required"`
	Reason *string          `json:"reason" validate:"omitempty,max=300"`
}

// ListUsersQuery binds the query string of GET /users: ?status=&stage=&branch_id=
// &q= plus standard page/limit/sort. The enum and UUID filters are bound as raw
// strings and parsed/validated in the service, because Gin's form binder does
// not run the enums' JSON validation or UUID parsing — doing it explicitly keeps
// a bad filter a clean 400 rather than a silently-empty result set. Q is a
// free-text search over email, username, employee code and profile names.
type ListUsersQuery struct {
	pagination.Offset

	Status   string `form:"status"`
	Stage    string `form:"stage"`
	BranchID string `form:"branch_id"`
	Q        string `form:"q"`
}

// BranchAssignmentRequest is the body of POST /users/{id}/branches: grant the
// user an additional branch within the caller's org. branch_id is required; an
// optional expires_at bounds the grant (RFC3339). A re-grant of an already-held
// branch refreshes its expiry rather than erroring (idempotent upsert). The
// grant is org-scoped server-side, so branch_id can never target another tenant's
// branch.
type BranchAssignmentRequest struct {
	BranchID  uuid.UUID  `json:"branch_id"  validate:"required"`
	ExpiresAt *time.Time `json:"expires_at" validate:"omitempty"`
}

// BranchAssignmentItem is one row of GET /users/{id}/branches: a single granted
// branch, when it was granted, by whom (nil when system-granted), and its optional
// expiry. It excludes the user's home branch (users.branch_id), which is separate
// and visible on the user record itself.
type BranchAssignmentItem struct {
	BranchID  uuid.UUID   `json:"branch_id"`
	GrantedBy *uuid.UUID  `json:"granted_by,omitempty"`
	GrantedAt time.Time   `json:"granted_at"`
	ExpiresAt *time.Time  `json:"expires_at,omitempty"`
}

// ListItem is the compact projection returned by GET /users — enough for a
// list/table view without the full profile payload of a detail read. FullName is
// computed in SQL (display name, else "first last", else email) so the list needs
// no second query per row.
type ListItem struct {
	ID           uuid.UUID        `json:"id"`
	Email        string           `json:"email"`
	Username     *string          `json:"username,omitempty"`
	FullName     string           `json:"full_name"`
	Status       enums.UserStatus `json:"status"`
	Stage        enums.UserStage  `json:"stage"`
	BranchID     *uuid.UUID       `json:"branch_id,omitempty"`
	EmployeeCode *string          `json:"employee_code,omitempty"`
	LastLoginAt  *time.Time       `json:"last_login_at,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
}
