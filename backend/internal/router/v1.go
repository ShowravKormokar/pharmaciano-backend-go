package router

import (
	"github.com/gin-gonic/gin"

	appctx "backend/internal/common/context"
	"backend/pkg/response"
)

// registerV1 mounts the versioned application API under /api/v1.
//
// Domain modules are not wired here directly (that would recreate the import
// cycle the middleware package was designed to avoid). Instead, once a module
// exists it exposes a `RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware)`
// function and the composition root calls it — or this function grows a small,
// explicit list of such calls. Until then this establishes the version prefix
// and a public, dependency-free status probe.
//
// # How a module mounts (the canonical pattern)
//
// Each protected route composes the chains from the middleware container in this
// order (rate-limit is outermost so throttling happens before any auth work):
//
//	grp := v1.Group("/sales")
//	// read
//	grp.GET("",     append(mw.Protected("sales", "read"),  saleHandler.List)...)
//	grp.GET("/:id", append(mw.Protected("sales", "read"),  saleHandler.Get)...)
//	// write — add idempotency + audit on the mutation, and a stricter limiter
//	grp.POST("",
//	    mw.RateLimit("sales_write"),
//	    append(mw.Protected("sales", "create"),
//	        mw.Idempotency(), mw.Audit(), saleHandler.Create)...,
//	)
//
// Pre-auth endpoints (login, refresh, password reset) use mw.RateLimitByIP
// instead of mw.RateLimit, because there is no principal yet to key a quota on.
func registerV1(engine *gin.Engine, d Deps) {
	v1 := engine.Group("/api/v1")

	// Public, unauthenticated liveness of the API surface itself (distinct from
	// /readyz, which reports dependency health). Handy for smoke tests and for
	// clients to confirm the version they are talking to.
	v1.GET("/status", func(c *gin.Context) {
		rid := appctx.RequestID(c.Request.Context())
		payload := gin.H{"status": "ok"}
		if d.Cfg != nil {
			payload["app"] = d.Cfg.App.Name
			payload["version"] = d.Cfg.App.Version
		}
		_ = response.OK(c.Writer, rid, payload)
	})

	// Domain module route groups are registered below as they come online, e.g.:
	//
	//	auth.RegisterRoutes(v1, d.MW)
	//	user.RegisterRoutes(v1, d.MW)
	//	inventory.RegisterRoutes(v1, d.MW)
	//
	// Keeping the mount points here (rather than scattered across modules'
	// init()) makes the full external URL surface auditable from one file.
}
