package rbac

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the role and permission endpoints under /api/v1.
//
// Chain order matches the canonical pattern (router/v1.go): rate-limit
// outermost, then Protected (auth → tenant → rbac); mutations additionally carry
// Idempotency + Audit innermost. Reads use the "auth_read" limiter policy,
// writes "auth_write".
//
// Authorization module/action strings come from the constants package — the same
// source the seed uses to build the permission catalogue — so a route can never
// be gated on a (module, action) pair that has no backing permission row.
//
// The user↔role assignment endpoints are deliberately absent: they are mounted
// by the user module at /users/{id}/roles (to keep a single :id wildcard name on
// the /users subtree) and call this module's Service. Mounting them here under a
// second :id-named subtree is unnecessary and the assignment resource is
// user-centric.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(module, action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(module, action), handler)...)
	}
	write := func(module, action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected(module, action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	roles := rg.Group("/roles")
	roles.POST("", write(constants.ModuleRoles, constants.ActionCreate, h.CreateRole)...)
	roles.GET("", read(constants.ModuleRoles, constants.ActionView, h.ListRoles)...)
	roles.GET("/:id", read(constants.ModuleRoles, constants.ActionView, h.GetRole)...)
	roles.PATCH("/:id", write(constants.ModuleRoles, constants.ActionUpdate, h.UpdateRole)...)
	roles.DELETE("/:id", write(constants.ModuleRoles, constants.ActionDelete, h.DeleteRole)...)
	// Managing a role's granted permissions is treated as updating the role.
	roles.GET("/:id/permissions", read(constants.ModuleRoles, constants.ActionView, h.ListRolePermissions)...)
	roles.PUT("/:id/permissions", write(constants.ModuleRoles, constants.ActionUpdate, h.SetRolePermissions)...)

	perms := rg.Group("/permissions")
	perms.POST("", write(constants.ModulePermissions, constants.ActionCreate, h.CreatePermission)...)
	perms.GET("", read(constants.ModulePermissions, constants.ActionView, h.ListPermissions)...)
	perms.GET("/:id", read(constants.ModulePermissions, constants.ActionView, h.GetPermission)...)
}
