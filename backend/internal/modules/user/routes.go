package user

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the user endpoints under the given /api/v1 group.
//
// Chain order matches the canonical pattern (router/v1.go): rate-limit
// outermost, then Protected (auth → tenant → rbac); mutations additionally carry
// Idempotency + Audit innermost. Reads use the "auth_read" limiter policy, writes
// "auth_write". Authorization module/action strings come from the constants
// package — the same source the seed uses to build the permission catalogue — so
// a route can never be gated on a (module, action) pair with no backing
// permission row.
//
// # Path-parameter naming contract
//
// Every route under /users/:id uses the parameter name "id"; the nested role
// routes add ":role_id" only at the deeper /users/:id/roles/:role_id position.
// Gin's radix router panics if one path position is registered with two
// different wildcard names, so anything hung off /users/{...} MUST reuse ":id"
// for the user segment.
//
// # /users/me
//
// The self endpoint is authenticated-only — RateLimit → Auth → Tenant, with no
// RBAC gate — so any signed-in user can read their own record without holding
// the admin-level users:view permission. It is registered before "/:id" for
// readability; Gin matches the static segment ahead of the param regardless of
// registration order.
//
// # User ↔ role assignments
//
// GET/POST /users/:id/roles and DELETE /users/:id/roles/:role_id are surfaced
// here (rather than in rbac) to keep one :id wildcard on the /users subtree; the
// handlers delegate to rbac through the service's role port. They are gated under
// the users module: viewing a user's roles is users:view, granting is
// users:assign, revoking is users:revoke.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(constants.ModuleUsers, action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected(constants.ModuleUsers, action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}
	// authed is an authenticated-only chain (no permission gate) for endpoints
	// scoped to the caller themselves.
	authed := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{mw.RateLimit("auth_read"), mw.Auth(), mw.Tenant(), handler}
	}

	grp := rg.Group("/users")
	grp.POST("", write(constants.ActionCreate, h.Create)...)
	grp.GET("", read(constants.ActionView, h.List)...)
	grp.GET("/me", authed(h.Me)...)
	grp.GET("/:id", read(constants.ActionView, h.Get)...)
	grp.PATCH("/:id", write(constants.ActionUpdate, h.Update)...)
	grp.PATCH("/:id/status", write(constants.ActionUpdate, h.ChangeStatus)...)
	grp.DELETE("/:id", write(constants.ActionDelete, h.Delete)...)

	grp.GET("/:id/roles", read(constants.ActionView, h.ListRoles)...)
	grp.POST("/:id/roles", write(constants.ActionAssign, h.AssignRole)...)
	grp.DELETE("/:id/roles/:role_id", write(constants.ActionRevoke, h.RevokeRole)...)

	grp.GET("/:id/branches", read(constants.ActionView, h.ListBranches)...)
	grp.POST("/:id/branches", write(constants.ActionAssign, h.AssignBranch)...)
	grp.DELETE("/:id/branches/:branch_id", write(constants.ActionRevoke, h.RevokeBranch)...)
}
