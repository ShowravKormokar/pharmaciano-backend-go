package router

import (
	"github.com/gin-gonic/gin"

	appctx "backend/internal/common/context"
	"backend/pkg/response"
)

func registerV1(engine *gin.Engine, d Deps) {
	registerVersion(engine, d, "v1", d.Modules)
}

// registerVersion keeps the URL-version boundary separate from the module
// implementations. A future v2 can pass a different module list while v1
// remains mounted and backward compatible.
func registerVersion(engine *gin.Engine, d Deps, version string, modules []ModuleRegistrar) {
	api := engine.Group("/api/" + version)

	// Public, unauthenticated liveness of the API surface itself (distinct from /readyz, which reports dependency health).
	api.GET("/status", func(c *gin.Context) {
		rid := appctx.RequestID(c.Request.Context())
		payload := gin.H{"status": "ok"}
		if d.Cfg != nil {
			payload["app"] = d.Cfg.App.Name
			payload["version"] = d.Cfg.App.Version
		}
		_ = response.OK(c.Writer, rid, payload)
	})

	// Domain modules, mounted in the order the composition root supplied them.
	for _, m := range modules {
		m.RegisterRoutes(api, d.MW)
	}
}
