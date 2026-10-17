package backup

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	if h == nil || h.svc == nil || !h.svc.routesAvailable() || rg == nil || mw == nil {
		return
	}

	grp := rg.Group("/backups")

	grp.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		c.Next()
	})

	grp.GET("",
		append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("backup", "view"), h.ListBackupRuns)...)...,
	)

	grp.GET("/:id",
		append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("backup", "view"), h.GetBackupRun)...)...,
	)

	grp.POST("",
		append([]gin.HandlerFunc{mw.RateLimit("backup")},
			append(mw.Protected("backup", "run"),
				mw.Idempotency(), h.CreateBackupRun)...)...,
	)

	// Verification, download, and deletion remain intentionally unregistered:
	// their step-up, isolated verification, authenticated streaming, and safe
	// retention/deletion prerequisites do not exist in the current runtime.
}
