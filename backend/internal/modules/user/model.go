// Package user implements the user module: the staff accounts that belong to an
// organization (and optionally a branch). It owns the *management* surface of
// the shared users table — create, list, read, edit, status lifecycle and
// soft-delete — plus the identity fields of user_profiles.
//
// The users table is deliberately shared with the auth module, which owns the
// *credential/authentication* surface (password verification, login tracking,
// MFA secrets, lockout). This module therefore never reads or writes those
// columns beyond a read-only display of a few flags: keeping the two concerns in
// separate repositories over one table means neither module can accidentally
// mutate the other's invariants.
//
// Tenancy is a data-layer contract: organization_id is bound from the caller's
// token (appctx.OrgID) on every read and write and is never taken from client
// input, so a query can only ever touch the caller's own tenant. Cross-tenant
// access surfaces as NotFound (never Forbidden) so a caller cannot probe for the
// existence of rows in other organizations.
package user

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
	"backend/internal/common/enums"
)

// User mirrors the managed subset of the users table (migrations/000004). Field
// order and db tags line up with userColumns so the repository can SELECT a
// fixed column list and Scan straight into this struct. Nullable columns are
// pointers.
//
// Secrets and auth-owned mutable state are excluded from this module's
// projection on purpose: mfa_secret_encrypted and salary_encrypted are never
// selected here (a dedicated, encryption-aware flow owns them), and
// last_login_ip / failed_attempts / locked_until / password_changed_at belong to
// the auth module. The few auth-owned flags kept here (MustChangePassword,
// MFAEnabled, LastLoginAt) are read-only display fields — this module never
// writes them.
type User struct {
	common.BaseModel

	// OrganizationID is always bound from the caller's token, never client input.
	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	BranchID       *uuid.UUID `db:"branch_id"        json:"branch_id,omitempty"`

	EmployeeCode *string `db:"employee_code" json:"employee_code,omitempty"`
	Email        string  `db:"email"         json:"email"`
	Username     *string `db:"username"      json:"username,omitempty"`
	Phone        *string `db:"phone"         json:"phone,omitempty"`

	// PasswordHash is write-only: the service sets it on create from a freshly
	// Argon2id-hashed secret. It is intentionally absent from userColumns (never
	// selected back) and json:"-" (never serialized); the auth module owns
	// credential reads and rotation.
	PasswordHash string `db:"password_hash" json:"-"`

	Status enums.UserStatus `db:"status" json:"status"`
	Stage  enums.UserStage  `db:"stage"  json:"stage"`

	// Auth-owned flags, surfaced read-only for admin views.
	MustChangePassword bool       `db:"must_change_password" json:"must_change_password"`
	MFAEnabled         bool       `db:"mfa_enabled"          json:"mfa_enabled"`
	LastLoginAt        *time.Time `db:"last_login_at"        json:"last_login_at,omitempty"`

	JoiningDate    *time.Time            `db:"joining_date"    json:"joining_date,omitempty"`
	EmploymentType *enums.EmploymentType `db:"employment_type" json:"employment_type,omitempty"`
}

// UserProfile mirrors the identity subset of user_profiles. The user module owns
// these human-identity fields only. Extended HR fields (father/mother name,
// blood group, marital status, nationality, religion, description) and the
// encrypted PII columns (NID, birth certificate, passport, TIN) are managed by a
// separate, encryption-aware profile flow and are deliberately outside this
// projection — so this module never has to decrypt them and cannot leak them.
type UserProfile struct {
	ID     uuid.UUID `db:"id"      json:"id"`
	UserID uuid.UUID `db:"user_id" json:"user_id"`

	FirstName   string  `db:"first_name"   json:"first_name"`
	LastName    string  `db:"last_name"    json:"last_name"`
	MiddleName  *string `db:"middle_name"  json:"middle_name,omitempty"`
	DisplayName *string `db:"display_name" json:"display_name,omitempty"`

	DateOfBirth *time.Time    `db:"date_of_birth" json:"date_of_birth,omitempty"`
	Gender      *enums.Gender `db:"gender"        json:"gender,omitempty"`
	AvatarURL   *string       `db:"avatar_url"    json:"avatar_url,omitempty"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// Detail is the full read model returned by the create/read endpoints: the user
// row with its (always-present) identity profile nested. User is embedded so its
// fields serialize at the top level of the JSON object, with the profile under
// "profile". Profile is a pointer purely for defensiveness — a user is always
// created with a profile in the same transaction, but a nil here degrades
// gracefully rather than panicking if a profile row is ever missing.
type Detail struct {
	User
	Profile *UserProfile `json:"profile,omitempty"`
}
