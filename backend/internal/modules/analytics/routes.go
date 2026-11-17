package analytics

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the gated sales-first Analytics read endpoints.
// Routes must remain unregistered by the application composition root until the
// analytics_* projections/migrations, frozen Sale outbox event contracts and
// producers, transactional consumer, reconciliation gates, and
// features.analytics_read_api enablement are satisfied. Additional endpoints in
// the contract (inventory, valuation, profitability, customer, exports) stay
// unexposed until their owner contracts, permissions, and gates pass.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(constants.ModuleAnalytics, constants.ActionView), handler)...)
	}

	group := rg.Group("/analytics")
	group.GET("/dashboard", read(h.GetDashboard)...)
	group.GET("/sales/overview", read(h.GetSalesOverview)...)
}
