package purchase

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the supported purchase-order read endpoints.
//
// Purchase writes are intentionally not registered: the current implementation
// lacks the schema/migration and transactional audit, outbox, and durable
// idempotency integrations required to expose mutations safely.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(constants.ModulePurchases, constants.ActionView), handler)...)
	}

	group := rg.Group("/purchases")
	group.GET("", read(h.List)...)
	group.GET("/:id", read(h.Get)...)
}
