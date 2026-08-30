package router

import (
	"github.com/gin-gonic/gin"

	appctx "backend/internal/common/context"
	"backend/pkg/response"
)

func registerV1(engine *gin.Engine, d Deps) {
	v1 := engine.Group("/api/v1")

	// Public, unauthenticated liveness of the API surface itself (distinct from /readyz, which reports dependency health).
	v1.GET("/status", func(c *gin.Context) {
		rid := appctx.RequestID(c.Request.Context())
		payload := gin.H{"status": "ok"}
		if d.Cfg != nil {
			payload["app"] = d.Cfg.App.Name
			payload["version"] = d.Cfg.App.Version
		}
		_ = response.OK(c.Writer, rid, payload)
	})

	// Domain modules, mounted in the order the composition root supplied them.
	for _, m := range d.Modules {
		m.RegisterRoutes(v1, d.MW)
	}
}
