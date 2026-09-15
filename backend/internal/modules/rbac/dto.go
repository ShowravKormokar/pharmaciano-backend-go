package rbac

import (
	"time"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// --- Roles -------------------------------------------------------------------

// CreateRoleRequest is the body of POST /roles. The role is always created
// tenant-owned (organization_id bound from the caller's token) and non-system;
// name is unique within the org (a conflict surfaces as 409 AlreadyExists).
type CreateRoleRequest struct {
	Name        string  `json:"name"        validate:"required,min=2,max=80"`
	Description *string `json:"description" validate:"omitempty,max=500"`
	// Priority ranks this role against others a user may hold; higher wins when
	// choosing the canonical role. Defaults to 0 when omitted.
	Priority *int `json:"priority" validate:"omitempty,gte=0,lte=1000"`
	// IsActive defaults to true when omitted — a freshly created role is usable.
	IsActive *bool `json:"is_active" validate:"omitempty"`
}

// UpdateRoleRequest is the body of PATCH /roles/{id}: every field optional, nil
// = leave unchanged. Renaming is permitted for tenant roles (the enforcer keys
// policies off the live role name via join, so a rename stays consistent after
// the next snapshot rebuild). System roles are immutable and rejected earlier.
type UpdateRoleRequest struct {
	Name        *string `json:"name"        validate:"omitempty,min=2,max=80"`
	Description *string `json:"description" validate:"omitempty,max=500"`
	IsActive    *bool   `json:"is_active"   validate:"omitempty"`
	Priority    *int    `json:"priority"    validate:"omitempty,gte=0,lte=1000"`
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op UPDATE.
func (r UpdateRoleRequest) IsEmpty() bool {
	return r == UpdateRoleRequest{}
}

// ListRolesQuery binds GET /roles?is_active=&page=&limit=&sort=. The listing
// always spans the caller's own roles plus the shared system roles; is_active
// optionally narrows to enabled (or disabled) roles.
type ListRolesQuery struct {
	pagination.Offset

	IsActive *bool `form:"is_active"`
}

// RoleListItem is the compact projection returned by GET /roles. IsGlobal marks
// a shared system role (organization_id NULL), which the client should render
// read-only.
type RoleListItem struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	IsActive    bool      `json:"is_active"`
	IsSystem    bool      `json:"is_system"`
	IsGlobal    bool      `json:"is_global"`
	Priority    int       `json:"priority"`
}

// RoleWithPermissions is the GET /roles/{id} detail: the role plus the exact set
// of permissions currently granted to it.
type RoleWithPermissions struct {
	Role
	Permissions []Permission `json:"permissions"`
}

// --- Permissions -------------------------------------------------------------

// CreatePermissionRequest is the body of POST /permissions. Permissions are part
// of the platform capability catalogue, so this is a SUPER_ADMIN-only escape
// hatch for registering a capability the seed matrix does not cover; the created
// row is non-system. The (module, action) pair is unique (409 on conflict).
type CreatePermissionRequest struct {
	Module      string  `json:"module"      validate:"required,min=2,max=60"`
	Action      string  `json:"action"      validate:"required,min=2,max=40"`
	Description *string `json:"description" validate:"omitempty,max=255"`
}

// ListPermissionsQuery binds GET /permissions?module=&page=&limit=&sort=. The
// module filter scopes the catalogue to one module's actions.
type ListPermissionsQuery struct {
	pagination.Offset

	Module *string `form:"module" validate:"omitempty,max=60"`
}

// PermissionListItem is the compact projection returned by GET /permissions.
type PermissionListItem struct {
	ID          uuid.UUID `json:"id"`
	Module      string    `json:"module"`
	Action      string    `json:"action"`
	Description *string   `json:"description,omitempty"`
	IsSystem    bool      `json:"is_system"`
}

// --- Role ↔ permission grants ------------------------------------------------

// SetRolePermissionsRequest is the body of PUT /roles/{id}/permissions. It
// replaces the role's entire grant set with exactly the listed permissions
// (idempotent): sending an empty list revokes every permission. Duplicate ids in
// the payload are de-duplicated by the repository's ON CONFLICT DO NOTHING.
type SetRolePermissionsRequest struct {
	PermissionIDs []uuid.UUID `json:"permission_ids" validate:"omitempty,max=500,dive,required"`
}

// --- User ↔ role assignments -------------------------------------------------
//
// Assignment endpoints are surfaced by the user module (/users/{id}/roles) but
// operate on rbac-owned data, so the request/response shapes live here next to
// the UserRole model and are consumed across the module boundary through the
// rbac service.

// AssignRoleRequest assigns one role to a user, optionally branch-scoped and/or
// time-limited. RoleID must reference a role visible to the caller's org (a
// tenant role of that org or a system role); a foreign role yields NotFound.
// A non-nil ExpiresAt is required to be in the future — enforced in the service
// rather than a validator tag, because "future" is relative to server time, not
// another request field.
type AssignRoleRequest struct {
	RoleID    uuid.UUID  `json:"role_id"    validate:"required"`
	BranchID  *uuid.UUID `json:"branch_id"  validate:"omitempty"`
	ExpiresAt *time.Time `json:"expires_at" validate:"omitempty"`
}

// UserRoleItem describes one of a user's role assignments for GET
// /users/{id}/roles. It joins the assignment to the role so the client sees the
// role name and privilege priority alongside the scope/expiry.
type UserRoleItem struct {
	RoleID     uuid.UUID  `json:"role_id"`
	Name       string     `json:"name"`
	Priority   int        `json:"priority"`
	IsSystem   bool       `json:"is_system"`
	BranchID   *uuid.UUID `json:"branch_id,omitempty"`
	AssignedAt time.Time  `json:"assigned_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}
