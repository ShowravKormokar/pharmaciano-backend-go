package supplier

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the supplier endpoints under the given /api/v1 group.
// Paths are relative to /api/v1 per SUPPLIER_CONTRACT.md §8.
// Chain order matches the canonical pattern: rate-limit outermost,
// then Protected (auth → tenant → rbac); mutations additionally carry
// Idempotency + Audit innermost. Reads use "auth_read", writes "auth_write".
// Permissions: suppliers:create, suppliers:view, suppliers:update, suppliers:delete.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("suppliers", action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("suppliers", action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	grp := rg.Group("/suppliers")
	grp.POST("", write("create", h.Create)...)
	grp.GET("", read("view", h.List)...)
	grp.GET("/:id", read("view", h.Get)...)
	grp.PATCH("/:id", write("update", h.Update)...)
	grp.DELETE("/:id", write("delete", h.Delete)...)
}