package audit

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{
			mw.RateLimitByIP("audit_read_ip"),
			mw.Auth(),
			mw.RateLimit("audit_read"),
			mw.IdleSession(),
			mw.Tenant(),
			mw.RBAC(constants.ModuleAudit, constants.ActionView),
			mw.AuditRead(),
			handler,
		}
	}

	group := rg.Group("/audit-logs")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		c.Header("Pragma", "no-cache")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	})
	group.GET("", read(h.List)...)
	group.GET("/modules", read(h.ListModules)...)
	group.GET("/actions", read(h.ListActions)...)
	group.GET("/:id", read(h.Get)...)

	// Platform events live outside tenant ownership and are additionally
	// restricted to SUPER_ADMIN by the service, even after route-level RBAC.
	group.GET("/platform", read(h.ListPlatformEvents)...)
	group.GET("/platform/:id", read(h.GetPlatformEvent)...)
}
