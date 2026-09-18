package branch

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the branch endpoints under the given /api/v1 group.
//
// Chain order matches the canonical pattern (router/v1.go): rate-limit outermost,
// then Protected (auth → tenant → rbac); mutations additionally carry
// Idempotency + Audit innermost. Reads use the "auth_read" policy, writes
// "auth_write".
//
// # Path-parameter naming contract
//
// Every route under /branches/:id uses the parameter name "id" — including the
// nested warehouses list the warehouse module mounts at
// /branches/:id/warehouses. Gin's radix router panics if the same path position
// is registered with two different wildcard names (":id" vs ":branch_id"), so
// all modules that hang routes off /branches/{...} MUST reuse ":id". The
// warehouse handler reads that segment as the branch id.
//
// # Endpoints owned by other modules (not registered here)
//
//	GET /branches/:id/users    → users module      (permission users:view)
//	GET /branches/:id/summary  → analytics module  (permission analytics:view)
//
// They are intentionally absent from this file: each module registers and
// authorizes its own routes, and both depend on domains (users, analytics) that
// this module must not import. Keeping them out avoids leaking a branch-scoped
// dependency into unrelated modules.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/branches")

	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("branches", action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("branches", action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	grp.POST("", write("create", h.Create)...)
	grp.GET("", read("view", h.List)...)
	grp.GET("/:id", read("view", h.Get)...)
	grp.PATCH("/:id", write("update", h.Update)...)
	grp.PUT("/:id", write("update", h.Replace)...)
	grp.DELETE("/:id", write("delete", h.Delete)...)
}
