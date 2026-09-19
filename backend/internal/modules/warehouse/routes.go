package warehouse

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the warehouse endpoints under the given /api/v1 group.
//
// Chain order matches the canonical pattern (router/v1.go): rate-limit outermost,
// then Protected (auth → tenant → rbac); mutations additionally carry
// Idempotency + Audit innermost. Reads use the "auth_read" policy, writes
// "auth_write". Every route is authorized against the "warehouses" module.
//
// # Path-parameter naming contract (shared with the branch module)
//
// The nested list is mounted at /branches/:id/warehouses, hanging off the same
// path position the branch module registers its /branches/:id routes under.
// Gin's radix router panics if one path position is registered with two
// different wildcard names, so this MUST reuse ":id" (not ":branch_id"); the
// handler reads that segment as the branch id. See branch/routes.go for the
// authoritative statement of this contract.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("warehouses", action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("warehouses", action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	grp := rg.Group("/warehouses")
	grp.POST("", write("create", h.Create)...)
	grp.GET("", read("view", h.List)...)
	grp.GET("/:id", read("view", h.Get)...)
	grp.PATCH("/:id", write("update", h.Update)...)
	grp.DELETE("/:id", write("delete", h.Delete)...)

	// Nested, branch-scoped read. ":id" is the branch id (see the contract above).
	branches := rg.Group("/branches")
	branches.GET("/:id/warehouses", read("view", h.ListByBranch)...)
}
