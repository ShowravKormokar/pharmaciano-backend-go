package rbac

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/internal/platform/telemetry"
	"backend/pkg/pagination"
)

// Service holds the rbac business logic: tenant binding, the system-role
// guardrails (reserved names, read-only system rows), grant/assignment
// validation, and — crucially — reloading the enforcer snapshot after any change
// so a policy edit takes effect immediately. It is the only layer that maps raw
// driver errors to domain *errs.AppError values.
type Service struct {
	repo    *Repository
	db      *db.DB
	enf     *Enforcer
	metrics *telemetry.Metrics
	log     *zap.Logger
	now     func() time.Time
}

// NewService assembles the service from its repository, the shared database
// handle (for the multi-statement transactions), and the enforcer it keeps warm.
// metrics may be nil; role/permission mutation paths no-op their counters in
// that case so unit tests can build a Service without a registry.
func NewService(repo *Repository, database *db.DB, enf *Enforcer, metrics *telemetry.Metrics, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, db: database, enf: enf, metrics: metrics, log: log, now: time.Now}
}

// reloadEnforcer refreshes the in-memory policy snapshot after a committed
// change. It is best-effort: the change is already durable, so a transient
// reload failure is logged and left for the periodic auto-reload to reconcile
// rather than failing the request that just succeeded. Call it only *after* the
// transaction commits, so the reload's reads observe the new state.
func (s *Service) reloadEnforcer(ctx context.Context) {
	if s.enf == nil {
		return
	}
	if err := s.enf.Load(ctx); err != nil {
		s.log.Error("rbac: enforcer reload after change failed; auto-reload will reconcile", zap.Error(err))
	}
}

// --- Roles -------------------------------------------------------------------

// CreateRole creates a tenant-owned, non-system role in the caller's org. The
// name may not collide with a reserved system-role name (which would let a
// tenant shadow SUPER_ADMIN et al. in the name-keyed enforcer). A duplicate name
// within the org is a 409.
func (s *Service) CreateRole(ctx context.Context, req *CreateRoleRequest) (*Role, error) {
	if isReservedRoleName(req.Name) {
		return nil, errs.Validation("role name is reserved for a system role",
			errs.FieldError{Field: "name", Rule: "reserved", Value: req.Name})
	}
	orgID := appctx.OrgID(ctx)
	role := &Role{
		OrganizationID: &orgID,
		Name:           req.Name,
		Description:    req.Description,
		IsActive:       derefBool(req.IsActive, true),
		IsSystem:       false,
		Priority:       derefInt(req.Priority, 0),
	}
	var out *Role
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		created, err := s.repo.InsertRole(ctx, role)
		if err != nil {
			return mapWriteErr(err, "role", "role name")
		}
		// A fresh role carries no grants and no members; advance the org
		// RBAC generation so the next snapshot reflects its existence even
		// if a future grant lands before the next per-user version bump.
		if err := s.repo.BumpOrgRBACGeneration(ctx, orgID); err != nil {
			return errs.DatabaseError(err)
		}
		s.observeGenerationBump("role_created")
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.reloadEnforcer(ctx)
	return out, nil
}

// GetRole returns a role visible to the caller (own tenant role or a shared
// system role) together with its current permission set, or NotFound.
func (s *Service) GetRole(ctx context.Context, id uuid.UUID) (*RoleWithPermissions, error) {
	orgID := appctx.OrgID(ctx)
	role, err := s.repo.FindRoleForView(ctx, orgID, id)
	if err != nil {
		return nil, mapReadErr(err, "role")
	}
	perms, err := s.repo.ListRolePermissions(ctx, role.ID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	return &RoleWithPermissions{Role: *role, Permissions: perms}, nil
}

// ListRoles returns a filtered, paginated page of roles visible to the caller.
func (s *Service) ListRoles(ctx context.Context, q *ListRolesQuery) ([]RoleListItem, pagination.Meta, error) {
	q.Offset.Normalize()
	items, total, err := s.repo.ListRoles(ctx, appctx.OrgID(ctx), RoleListFilter{IsActive: q.IsActive}, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// UpdateRole applies a partial change to a tenant role under a row lock. System
// roles are read-only (403) and foreign/unknown ids are NotFound; the two are
// disambiguated without leaking foreign tenant roles. A rename to a reserved
// system name is rejected. An empty patch degrades to a managed read.
func (s *Service) UpdateRole(ctx context.Context, id uuid.UUID, req *UpdateRoleRequest) (*Role, error) {
	orgID := appctx.OrgID(ctx)
	if req.Name != nil && isReservedRoleName(*req.Name) {
		return nil, errs.Validation("role name is reserved for a system role",
			errs.FieldError{Field: "name", Rule: "reserved", Value: *req.Name})
	}

	var out *Role
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindManagedRoleForUpdate(ctx, orgID, id)
		if err != nil {
			return s.classifyManagedRoleErr(ctx, orgID, id, err, "system roles are read-only")
		}
		if req.IsEmpty() {
			out = current
			return nil
		}
		applyRolePatch(current, req)
		saved, err := s.repo.UpdateRole(ctx, orgID, current)
		if err != nil {
			return mapWriteErr(err, "role", "role name")
		}
		if err := s.repo.BumpRoleMembersAuthzVersion(ctx, id); err != nil {
			return errs.DatabaseError(err)
		}
		if err := s.repo.BumpOrgRBACGeneration(ctx, orgID); err != nil {
			return errs.DatabaseError(err)
		}
		s.observeUserAuthzBump()
		s.observeGenerationBump("role_updated")
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !req.IsEmpty() {
		s.reloadEnforcer(ctx) // name/priority/active all change what the enforcer resolves
	}
	return out, nil
}

// DeleteRole soft-deletes a tenant role. System roles cannot be deleted (403);
// missing/foreign ids are NotFound. Grants and assignments to the role become
// inert automatically (the snapshot loaders exclude deleted roles).
func (s *Service) DeleteRole(ctx context.Context, id uuid.UUID) error {
	orgID := appctx.OrgID(ctx)
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		// Confirm ownership before invalidating members; this preserves the
		// no-cross-tenant-information-leak behavior of the delete endpoint.
		if _, err := s.repo.FindManagedRoleForUpdate(ctx, orgID, id); err != nil {
			return s.classifyManagedRoleErr(ctx, orgID, id, err, "system roles cannot be deleted")
		}
		if err := s.repo.BumpRoleMembersAuthzVersion(ctx, id); err != nil {
			return errs.DatabaseError(err)
		}
		if err := s.repo.BumpOrgRBACGeneration(ctx, orgID); err != nil {
			return errs.DatabaseError(err)
		}
		s.observeUserAuthzBump()
		s.observeGenerationBump("role_deleted")
		ok, err := s.repo.SoftDeleteRole(ctx, orgID, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !ok {
			return errs.NotFound("role")
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.reloadEnforcer(ctx)
	return nil
}

// --- Permissions -------------------------------------------------------------

// CreatePermission registers a non-system capability in the global catalogue.
// This is the escape hatch for a capability the seed matrix does not cover; the
// route is permission-gated (typically SUPER_ADMIN). A duplicate (module,
// action) is a 409. No reload: an ungranted permission changes no decision.
func (s *Service) CreatePermission(ctx context.Context, req *CreatePermissionRequest) (*Permission, error) {
	p := &Permission{
		Module:      req.Module,
		Action:      req.Action,
		Description: req.Description,
		IsSystem:    false,
	}
	created, err := s.repo.InsertPermission(ctx, p)
	if err != nil {
		return nil, mapWriteErr(err, "permission", "permission")
	}
	return created, nil
}

// GetPermission returns one permission from the catalogue, or NotFound.
func (s *Service) GetPermission(ctx context.Context, id uuid.UUID) (*Permission, error) {
	p, err := s.repo.FindPermissionByID(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, "permission")
	}
	return p, nil
}

// ListPermissions returns a filtered, paginated page of the capability catalogue.
func (s *Service) ListPermissions(ctx context.Context, q *ListPermissionsQuery) ([]PermissionListItem, pagination.Meta, error) {
	q.Offset.Normalize()
	items, total, err := s.repo.ListPermissions(ctx, PermissionListFilter{Module: q.Module}, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// --- Role ↔ permission grants ------------------------------------------------

// SetRolePermissions replaces a tenant role's entire grant set with exactly the
// requested permissions (idempotent; empty list revokes all). System roles are
// read-only. Unknown permission ids are rejected as a validation error. The
// lock, validation and replacement run in one transaction so no reader sees a
// half-swapped grant set; the enforcer is reloaded afterwards.
func (s *Service) SetRolePermissions(ctx context.Context, roleID uuid.UUID, req *SetRolePermissionsRequest) (*RoleWithPermissions, error) {
	orgID := appctx.OrgID(ctx)
	actor := appctx.UserID(ctx)
	unique := dedupeIDs(req.PermissionIDs)

	var out *RoleWithPermissions
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		role, err := s.repo.FindManagedRoleForUpdate(ctx, orgID, roleID)
		if err != nil {
			return s.classifyManagedRoleErr(ctx, orgID, roleID, err, "system role permissions are read-only")
		}
		if len(unique) > 0 {
			found, err := s.repo.FindPermissionsByIDs(ctx, unique)
			if err != nil {
				return errs.DatabaseError(err)
			}
			if len(found) != len(unique) {
				return errs.Validation("permission_ids contains one or more unknown permissions",
					errs.FieldError{Field: "permission_ids", Rule: "exists"})
			}
		}
		actorID := actor
		if err := s.repo.ReplaceRolePermissions(ctx, roleID, unique, &actorID); err != nil {
			return mapWriteErr(err, "role permissions", "permission")
		}
		if err := s.repo.BumpRoleMembersAuthzVersion(ctx, roleID); err != nil {
			return errs.DatabaseError(err)
		}
		if err := s.repo.BumpOrgRBACGeneration(ctx, orgID); err != nil {
			return errs.DatabaseError(err)
		}
		s.observeUserAuthzBump()
		s.observeGenerationBump("role_permissions_set")
		perms, err := s.repo.ListRolePermissions(ctx, roleID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		out = &RoleWithPermissions{Role: *role, Permissions: perms}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.reloadEnforcer(ctx)
	return out, nil
}

// --- User ↔ role assignments -------------------------------------------------

// AssignRole grants a role to a user in the caller's org (upsert: re-assigning
// refreshes scope/expiry). It validates, in one transaction, that the target
// user belongs to the org, the role is assignable (own tenant role or system
// role, active), and any branch scope belongs to the org — each failure a
// NotFound that never reveals another tenant's ids. A non-nil expiry must be in
// the future. Returns the assignment as it now stands.
func (s *Service) AssignRole(ctx context.Context, userID uuid.UUID, req *AssignRoleRequest) (*UserRoleItem, error) {
	orgID := appctx.OrgID(ctx)
	actor := appctx.UserID(ctx)

	if req.ExpiresAt != nil && !req.ExpiresAt.After(s.now()) {
		return nil, errs.Validation("expires_at must be in the future",
			errs.FieldError{Field: "expires_at", Rule: "future"})
	}

	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		okUser, err := s.repo.UserBelongsToOrg(ctx, orgID, userID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !okUser {
			return errs.NotFound("user")
		}

		okRole, err := s.repo.AssignableRoleExists(ctx, orgID, req.RoleID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !okRole {
			return errs.NotFound("role")
		}

		if req.BranchID != nil {
			okBranch, err := s.repo.BranchBelongsToOrg(ctx, orgID, *req.BranchID)
			if err != nil {
				return errs.DatabaseError(err)
			}
			if !okBranch {
				return errs.NotFound("branch")
			}
		}

		actorID := actor
		ur := &UserRole{
			UserID:     userID,
			RoleID:     req.RoleID,
			BranchID:   req.BranchID,
			AssignedBy: &actorID,
			ExpiresAt:  req.ExpiresAt,
		}
		if err := s.repo.AssignUserRole(ctx, ur); err != nil {
			return mapWriteErr(err, "role assignment", "role assignment")
		}
		if err := s.repo.BumpUserAuthzVersion(ctx, userID); err != nil {
			return errs.DatabaseError(err)
		}
		s.observeUserAuthzBump()
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.reloadEnforcer(ctx)
	return s.findUserRoleItem(ctx, userID, req.RoleID)
}

// RevokeRole removes a role from a user in the caller's org. A missing user or
// assignment is NotFound. Like every other role mutation it runs in one
// transaction so the authz_version bump commits atomically with the DELETE: a
// bump that fails after the revocation would otherwise leave the user's versioned
// authorization cache entry reachable (same key) and keep granting the revoked
// privilege until TTL.
func (s *Service) RevokeRole(ctx context.Context, userID, roleID uuid.UUID) error {
	orgID := appctx.OrgID(ctx)
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		okUser, err := s.repo.UserBelongsToOrg(ctx, orgID, userID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !okUser {
			return errs.NotFound("user")
		}
		ok, err := s.repo.RevokeUserRole(ctx, userID, roleID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !ok {
			return errs.NotFound("role assignment")
		}
		if err := s.repo.BumpUserAuthzVersion(ctx, userID); err != nil {
			return errs.DatabaseError(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.observeUserAuthzBump()
	s.reloadEnforcer(ctx)
	return nil
}

// ListRolesOfUser returns a user's role assignments (in the caller's org),
// highest privilege first. A user outside the org is NotFound.
func (s *Service) ListRolesOfUser(ctx context.Context, userID uuid.UUID) ([]UserRoleItem, error) {
	orgID := appctx.OrgID(ctx)
	okUser, err := s.repo.UserBelongsToOrg(ctx, orgID, userID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	if !okUser {
		return nil, errs.NotFound("user")
	}
	items, err := s.repo.ListUserRoles(ctx, userID)
	if err != nil {
		return nil, errs.DatabaseError(err)
	}
	return items, nil
}

// --- Helpers -----------------------------------------------------------------

// classifyManagedRoleErr turns a "managed role not found" outcome into the right
// domain error: a shared system role (public across tenants) is a read-only 403
// with the supplied message; anything else is a plain NotFound that never
// discloses whether a foreign tenant's role exists. A non-ErrNoRows error is a
// database error.
func (s *Service) classifyManagedRoleErr(ctx context.Context, orgID, id uuid.UUID, err error, systemMsg string) error {
	if !errors.Is(err, db.ErrNoRows) {
		return errs.DatabaseError(err)
	}
	if role, verr := s.repo.FindRoleForView(ctx, orgID, id); verr == nil && role.OrganizationID == nil {
		return errs.Forbidden(systemMsg)
	}
	return errs.NotFound("role")
}

// findUserRoleItem reads a single assignment back (joined to the role) after a
// write, so the handler can return the created/updated row. It reads from the
// primary because the write just committed and must be immediately observable —
// the replica path could lag and spuriously report the row as vanished.
func (s *Service) findUserRoleItem(ctx context.Context, userID, roleID uuid.UUID) (*UserRoleItem, error) {
	it, err := s.repo.FindUserRoleItemByPrimary(ctx, userID, roleID)
	if err != nil {
		if errors.Is(err, db.ErrNoRows) {
			// The row was just written in a committed tx; its absence here would be a
			// consistency bug, not a client error.
			return nil, errs.Internal(errors.New("rbac: assignment vanished after write"))
		}
		return nil, errs.DatabaseError(err)
	}
	return it, nil
}

// applyRolePatch copies the set (non-nil) fields of a PATCH onto r. Description
// is a pointer column: a non-nil pointer (even to "") overwrites, nil leaves it.
func applyRolePatch(r *Role, req *UpdateRoleRequest) {
	if req.Name != nil {
		r.Name = *req.Name
	}
	if req.Description != nil {
		r.Description = req.Description
	}
	if req.IsActive != nil {
		r.IsActive = *req.IsActive
	}
	if req.Priority != nil {
		r.Priority = *req.Priority
	}
}

// isReservedRoleName reports whether name matches a seeded system role name
// (case-insensitively). Tenants may not create/rename a role to these, because
// the enforcer keys policies by role name and a collision would let a tenant
// role inherit a system role's global ("*"-domain) grants.
func isReservedRoleName(name string) bool {
	for _, sysName := range constants.SystemRoles {
		if strings.EqualFold(name, sysName) {
			return true
		}
	}
	return false
}

// dedupeIDs returns ids with duplicates removed, preserving first-seen order.
func dedupeIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// mapReadErr maps a repository read error: missing row → NotFound(entity), else DB error.
func mapReadErr(err error, entity string) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound(entity)
	}
	return errs.DatabaseError(err)
}

// mapWriteErr maps a repository write error: missing row on RETURNING → NotFound;
// unique violation → AlreadyExists(dupResource); FK violation → a validation
// error (a referenced id does not exist); anything else → DatabaseError.
func mapWriteErr(err error, entity, dupResource string) error {
	switch {
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound(entity)
	case db.IsUniqueVilation(err):
		return errs.AlreadyExists(dupResource)
	case db.IsForeignKeyViolation(err):
		return errs.Validation(entity + " references a record that does not exist")
	default:
		return errs.DatabaseError(err)
	}
}

// derefBool returns *p if non-nil, otherwise def.
func derefBool(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

// derefInt returns *p if non-nil, otherwise def.
func derefInt(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

// observeGenerationBump is a nil-safe forward to the rbac_generation_bumps
// counter, one of the Authorization observability events called out in ADR
// §34. reason is a short, bounded bucket name — never the role id.
func (s *Service) observeGenerationBump(reason string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveRBACGenerationBump(reason)
}

// observeUserAuthzBump is a nil-safe forward to the authz_version_bumps
// (scope=user) counter.
func (s *Service) observeUserAuthzBump() {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveAuthzVersionBump("user")
}

// observeOrgAuthzBump is a nil-safe forward to the authz_version_bumps
// (scope=org) counter. Kept separate from the generation counter for ADR
// observability so a single edge-driven mutation can be isolated from a
// mass-user re-bump.
func (s *Service) observeOrgAuthzBump() {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.ObserveAuthzVersionBump("org")
}
