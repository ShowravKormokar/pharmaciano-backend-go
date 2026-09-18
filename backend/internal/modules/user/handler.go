package user

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/modules/rbac"
	"backend/internal/platform/db"
	"backend/internal/platform/validator"
	"backend/pkg/crypto"
)

// Handler is the HTTP edge of the user module: it binds and validates input,
// delegates to the Service, and renders the result or error through httpx. The
// user↔role assignment endpoints (/users/{id}/roles) live here too — they are
// user-centric routes whose data is owned by rbac, reached through the Service's
// role port.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// New assembles the user module (repository → service → handler) and returns the
// handler, which also carries route registration.
//
// Unlike a self-contained leaf module, user depends on three cross-module ports
// that the composition root resolves (construction order rbac → auth → user):
//
//   - roles (satisfied by *rbac.Service) backs the /users/{id}/roles endpoints.
//   - revoker (satisfied by the auth module) lets a status change or delete
//     terminate the user's sessions. It may be nil before auth is wired, in
//     which case such changes proceed but log a warning instead of revoking.
//   - bumper (satisfied by the auth module) advances the authz epoch on a branch
//     assignment grant/revoke so stale tokens lose the old scope immediately. It
//     may be nil before auth is wired, in which case assignments proceed but log
//     a warning instead of bumping.
func New(database *db.DB, v *validator.Validator, hasher crypto.PasswordHash, roles RoleService, revoker SessionRevoker, bumper AuthzBumper, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	repo := NewRepository(database)
	svc := NewService(repo, database, hasher, roles, revoker, bumper, log)
	return &Handler{svc: svc, val: v, log: log}
}

// Create handles POST /users: provision a new user + identity profile. Gated on
// users:create (seeded to SUPER_ADMIN/ADMIN), which is how the "only SUPER_ADMIN
// creates users" rule is enforced — as a tunable RBAC grant, not a hard-coded
// role check, so the policy stays coherent with the rest of authorization.
func (h *Handler) Create(c *gin.Context) {
	var req CreateUserRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	u, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, u)
}

// List handles GET /users?status=&stage=&branch_id=&q=&page=&limit=&sort=.
func (h *Handler) List(c *gin.Context) {
	var q ListUsersQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.List(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, items, meta)
}

// Me handles GET /users/me: the authenticated caller's own record and profile.
// It is authenticated-only (no users:view permission gate), so any signed-in
// user can read themselves; the subject comes from the principal, not the path.
func (h *Handler) Me(c *gin.Context) {
	u, err := h.svc.GetMe(c.Request.Context())
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, u)
}

// Get handles GET /users/{id}: one user in the caller's org, with profile.
func (h *Handler) Get(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	u, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, u)
}

// Update handles PATCH /users/{id}: partial update of the user and/or profile.
func (h *Handler) Update(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateUserRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	u, err := h.svc.Update(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, u)
}

// ChangeStatus handles PATCH /users/{id}/status: transition the lifecycle status.
// A move out of a login-capable status revokes the user's sessions server-side.
func (h *Handler) ChangeStatus(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req ChangeStatusRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	u, err := h.svc.ChangeStatus(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, u)
}

// Delete handles DELETE /users/{id}: soft-delete a user. The service revokes the
// user's active sessions in the same transaction, so a removed user loses access
// immediately, and their email/username/employee code become reusable at once.
func (h *Handler) Delete(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ListRoles handles GET /users/{id}/roles: the roles assigned to a user.
func (h *Handler) ListRoles(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, err := h.svc.ListRoles(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, items)
}

// AssignRole handles POST /users/{id}/roles: grant a role to a user (upsert;
// re-assigning refreshes scope/expiry). The body is rbac's AssignRoleRequest,
// validated at the edge; the service delegates to rbac, which enforces tenancy.
func (h *Handler) AssignRole(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req rbac.AssignRoleRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	item, err := h.svc.AssignRole(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, item)
}

// RevokeRole handles DELETE /users/{id}/roles/{role_id}: remove a role from a
// user (soft revoke → 204).
func (h *Handler) RevokeRole(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	roleID, err := parseIDParam(c, "role_id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.RevokeRole(c.Request.Context(), id, roleID); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// ListBranches handles GET /users/{id}/branches: the additional branches the user
// may act on in the caller's org (excluding the home branch, which lives on the
// user record). Gated on users:view like the rest of the read path.
func (h *Handler) ListBranches(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, err := h.svc.ListBranches(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, items)
}

// AssignBranch handles POST /users/{id}/branches: grant the user an additional
// branch in the caller's org. The service resolves the target user in-tenant,
// validates the branch, and bumps the user's authz_version so the new scope is
// enforced immediately.
func (h *Handler) AssignBranch(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req BranchAssignmentRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.AssignBranch(c.Request.Context(), id, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// RevokeBranch handles DELETE /users/{id}/branches/{branch_id}: remove the user's
// grant to branchID. The service bumps the user's authz_version so the stale scope
// is lost immediately. A grant that does not exist is 404.
func (h *Handler) RevokeBranch(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	branchID, err := parseIDParam(c, "branch_id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.RevokeBranch(c.Request.Context(), id, branchID); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// parseIDParam extracts and validates a UUID path parameter, returning a 400
// VALIDATION_ERROR (not 404) when the segment is malformed so a bad id is never
// confused with a missing resource.
func parseIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, errs.Validation("path parameter " + name + " must be a valid UUID").WithCause(err)
	}
	return id, nil
}
