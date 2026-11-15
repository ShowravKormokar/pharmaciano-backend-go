package sale

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts only read endpoints. It is intentionally not wired
// into the application composition root until the Sale schema and release
// gates documented in SALE_CONTRACT.md are satisfied.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(constants.ModuleSales, constants.ActionView), handler)...)
	}

	group := rg.Group("/sales")
	group.GET("", read(h.List)...)
	group.GET("/:id", read(h.Get)...)
	group.GET("/:id/invoice", read(h.GetInvoice)...)
}
