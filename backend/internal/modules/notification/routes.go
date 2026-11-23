package notification

import (
	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	"backend/internal/middleware"
)

// RegisterRoutes mounts the durable in-app inbox and broadcast API under
// /api/v1/notifications. Real-time (Phase 4) routes are intentionally not
// mounted here; polling remains the authoritative delivery path.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	group := rg.Group("/notifications")

	read := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected(constants.ModuleNotifications, constants.ActionView), handler)...)
	}
	write := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected(constants.ModuleNotifications, constants.ActionRead), handler)...)
	}
	dismiss := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected(constants.ModuleNotifications, constants.ActionDismiss), handler)...)
	}
	broadcast := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("broadcast")},
			append(mw.Protected(constants.ModuleNotifications, constants.ActionBroadcast), handler)...)
	}

	group.GET("", read(h.List)...)
	group.GET("/unread-count", read(h.UnreadCount)...)
	group.POST("/:id/read", write(h.Read)...)
	group.POST("/read-all", write(h.ReadAll)...)
	group.DELETE("/:id", dismiss(h.Dismiss)...)
	group.POST("/broadcast", broadcast(h.CreateBroadcast)...)
}
