package rbac

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"backend/internal/common/httpx"
	errs "backend/internal/errors"
	"backend/internal/platform/validator"
)

// Handler is the HTTP edge of the rbac module for the role and permission
// resources. It binds and validates input, delegates to the Service, and renders
// the result or error through httpx. The user↔role *assignment* endpoints
// (/users/{id}/roles) are intentionally not here: they are surfaced by the user
// module (to keep Gin's :id wildcard consistent under /users) but call this
// module's Service. See module.go for how the Service is shared.
type Handler struct {
	svc *Service
	val *validator.Validator
	log *zap.Logger
}

// NewHandler builds the HTTP handler over an already-assembled Service. The
// module's New (module.go) wires the repository → enforcer → service graph and
// calls this; it is separate from the leaf-module New pattern because rbac must
// also hand its enforcer and seeder to the composition root.
func NewHandler(svc *Service, v *validator.Validator, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{svc: svc, val: v, log: log}
}

// --- Roles -------------------------------------------------------------------

// CreateRole handles POST /roles: creates a tenant-owned, non-system role.
func (h *Handler) CreateRole(c *gin.Context) {
	var req CreateRoleRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	role, err := h.svc.CreateRole(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, role)
}

// ListRoles handles GET /roles?is_active=&page=&limit=&sort= — the caller's own
// roles plus the shared system roles.
func (h *Handler) ListRoles(c *gin.Context) {
	var q ListRolesQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListRoles(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, items, meta)
}

// GetRole handles GET /roles/{id}: the role plus its current permission set.
func (h *Handler) GetRole(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	role, err := h.svc.GetRole(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, role)
}

// UpdateRole handles PATCH /roles/{id}: partial update of a tenant role.
func (h *Handler) UpdateRole(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req UpdateRoleRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	role, err := h.svc.UpdateRole(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, role)
}

// DeleteRole handles DELETE /roles/{id} (soft-delete → 204).
func (h *Handler) DeleteRole(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	if err := h.svc.DeleteRole(c.Request.Context(), id); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.NoContent(c)
}

// --- Role ↔ permission grants ------------------------------------------------

// ListRolePermissions handles GET /roles/{id}/permissions: the exact set of
// permissions currently granted to a visible role. It reuses the visibility
// check in GetRole (own tenant role or a shared system role) and returns just
// the permission array.
func (h *Handler) ListRolePermissions(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	role, err := h.svc.GetRole(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, role.Permissions)
}

// SetRolePermissions handles PUT /roles/{id}/permissions: replaces the role's
// entire grant set with exactly the listed permissions (empty list revokes all).
func (h *Handler) SetRolePermissions(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	var req SetRolePermissionsRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	role, err := h.svc.SetRolePermissions(c.Request.Context(), id, &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, role)
}

// --- Permissions -------------------------------------------------------------

// CreatePermission handles POST /permissions: registers a non-system capability
// in the global catalogue (the escape hatch for a capability the seed does not
// cover).
func (h *Handler) CreatePermission(c *gin.Context) {
	var req CreatePermissionRequest
	if err := httpx.BindJSON(c, h.val, &req); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	p, err := h.svc.CreatePermission(c.Request.Context(), &req)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.Created(c, p)
}

// ListPermissions handles GET /permissions?module=&page=&limit=&sort=.
func (h *Handler) ListPermissions(c *gin.Context) {
	var q ListPermissionsQuery
	if err := httpx.BindQuery(c, h.val, &q); err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	items, meta, err := h.svc.ListPermissions(c.Request.Context(), &q)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.List(c, items, meta)
}

// GetPermission handles GET /permissions/{id}.
func (h *Handler) GetPermission(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	p, err := h.svc.GetPermission(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, h.log, err)
		return
	}
	httpx.OK(c, p)
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
