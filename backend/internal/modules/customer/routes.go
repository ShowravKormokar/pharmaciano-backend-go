package customer

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the customer endpoints under the given /api/v1 group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/customers")

	read := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("customers", action), handler)...)
	}
	write := func(action string, handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("customers", action),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	grp.POST("", write("create", h.Create)...)
	grp.GET("", read("view", h.List)...)
	grp.GET("/:id", read("view", h.Get)...)
	grp.PATCH("/:id", write("update", h.Update)...)
	grp.DELETE("/:id", write("delete", h.Archive)...)

	// Consents
	grp.GET("/:id/consents", read("view", h.ListConsents)...)
	grp.POST("/:id/consents", write("update", h.AddConsent)...)

	// Branch visibility
	grp.GET("/:id/branches", read("view", h.GetBranchVisibility)...)
	grp.POST("/:id/branches", write("update", h.GrantBranchVisibility)...)
	grp.DELETE("/:id/branches", write("delete", h.RevokeBranchVisibility)...)
}
