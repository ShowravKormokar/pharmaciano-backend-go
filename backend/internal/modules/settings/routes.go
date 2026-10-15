package settings

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	read := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("settings", "view"), handler)...)
	}
	write := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("settings", "update"),
				mw.Idempotency(), mw.Audit(), handler)...)
	}

	group := rg.Group("/settings")
	group.GET("", read(h.List)...)
	group.POST("/bulk", write(h.BulkUpdate)...)
	group.GET("/:key", read(h.Get)...)
	group.PUT("/:key", write(h.Update)...)
}
