// Package rbac implements role-based access control for the platform: the
// roles a tenant defines, the fixed catalogue of permissions (module:action
// pairs) the codebase understands, the grants that tie the two together, and
// the assignments that give users their roles.
//
// It also owns the authorization *decision*: an in-process enforcer (casbin.go)
// that faithfully implements config/casbin_model.conf and is exposed to the
// HTTP edge through the middleware.Authorizer port. rbac is therefore both a
// CRUD module (roles/permissions endpoints) and the engine every Protected
// route consults on the request hot path.
//
// # Table ownership
//
// rbac owns roles, permissions, role_permissions and user_roles (migrations
// 000003 and the user_roles slice of 000004). It never writes the users table —
// that belongs to the user module — but it joins to it read-only when building
// the enforcer's grouping snapshot (a user's org is the enforcement domain).
package rbac

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// Role is a named bundle of permissions a user can be assigned. Field order and
// db tags mirror migrations/000003 (roles) exactly so the repository can SELECT
// a fixed column list and Scan straight into this struct.
//
// A role is either tenant-owned or system-defined, distinguished by
// OrganizationID:
//
//   - OrganizationID != nil — a custom role created by that organization; only
//     it may see, edit or delete the role.
//   - OrganizationID == nil — a system role (SUPER_ADMIN, ADMIN, …) seeded once
//     and shared by every tenant. System roles are read-only through the API
//     (IsSystem is true) and their permissions apply in every domain (the
//     enforcer maps a nil org to the wildcard domain "*").
//
// The partial unique index ux_roles_org_name enforces name uniqueness per org
// (treating nil as the zero UUID), so a duplicate name surfaces as a unique
// violation the service translates to 409 AlreadyExists.
type Role struct {
	common.BaseModel

	// OrganizationID scopes the role to one tenant; nil marks a shared system
	// role. It is bound from the caller's token on create, never from the body.
	OrganizationID *uuid.UUID `db:"organization_id" json:"organization_id,omitempty"`

	Name        string  `db:"name"        json:"name"`
	Description *string `db:"description" json:"description,omitempty"`

	// IsActive gates the role in the enforcer: an inactive role grants nothing
	// and is excluded from the policy/grouping snapshot, without deleting the
	// grant history. IsSystem marks the seeded, API-immutable roles.
	IsActive bool `db:"is_active" json:"is_active"`
	IsSystem bool `db:"is_system" json:"is_system"`

	// Priority ranks roles when a user holds several: the highest-priority active
	// role becomes the Principal's canonical RoleName (used by the SUPER_ADMIN /
	// org-wide fast paths). Higher wins.
	Priority int `db:"priority" json:"priority"`
}

// Permission is one atomic capability, named by the (module, action) pair the
// RBAC middleware passes to Enforce (e.g. "sales" + "create"). The catalogue is
// seeded from the constants.AllModules × AllActions matrix and is effectively
// closed: permissions are system-defined (IsSystem defaults true) and referenced
// by role grants, never created ad hoc per request.
//
// Field order and db tags mirror migrations/000003 (permissions); the unique
// (module, action) constraint makes a duplicate a 409 AlreadyExists.
type Permission struct {
	common.BaseModel

	Module      string  `db:"module"      json:"module"`
	Action      string  `db:"action"      json:"action"`
	Description *string `db:"description" json:"description,omitempty"`
	IsSystem    bool    `db:"is_system"   json:"is_system"`
}

// Key returns the canonical "module:action" string used throughout the auth
// stack — the flat permission tokens carried on a Principal and matched by
// appctx.HasPermission. Keeping the format in one place guarantees the grant
// side and the check side never drift.
func (p Permission) Key() string { return p.Module + ":" + p.Action }

// RolePermission is the many-to-many grant tying a role to a permission. It is a
// pure join row: no surrogate id and no soft-delete (the composite primary key
// (role_id, permission_id) is the identity, and revoking is a hard DELETE, so a
// re-grant is a plain insert). GrantedBy is nullable because system grants seeded
// before any user exists have no actor.
type RolePermission struct {
	RoleID       uuid.UUID  `db:"role_id"       json:"role_id"`
	PermissionID uuid.UUID  `db:"permission_id" json:"permission_id"`
	GrantedAt    time.Time  `db:"granted_at"    json:"granted_at"`
	GrantedBy    *uuid.UUID `db:"granted_by"    json:"granted_by,omitempty"`
}

// UserRole assigns a role to a user, optionally scoped to a branch and/or given
// an expiry. Like RolePermission it is a join row keyed by (user_id, role_id)
// with no surrogate id or soft-delete.
//
//   - BranchID nil — the assignment applies org-wide; non-nil narrows it to one
//     branch (the branch-scope contract enforced by the tenant middleware, not
//     the obj/act matcher).
//   - ExpiresAt nil — the assignment never expires; a past ExpiresAt drops the
//     grant from the enforcer snapshot without needing a delete.
type UserRole struct {
	UserID     uuid.UUID  `db:"user_id"     json:"user_id"`
	RoleID     uuid.UUID  `db:"role_id"     json:"role_id"`
	BranchID   *uuid.UUID `db:"branch_id"   json:"branch_id,omitempty"`
	AssignedAt time.Time  `db:"assigned_at" json:"assigned_at"`
	AssignedBy *uuid.UUID `db:"assigned_by" json:"assigned_by,omitempty"`
	ExpiresAt  *time.Time `db:"expires_at"  json:"expires_at,omitempty"`
}
