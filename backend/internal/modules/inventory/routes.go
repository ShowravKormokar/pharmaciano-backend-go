package inventory

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/inventory")
	grp.GET("/batches",
		append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("inventory", "view"), h.ListBatches)...)...)
}
