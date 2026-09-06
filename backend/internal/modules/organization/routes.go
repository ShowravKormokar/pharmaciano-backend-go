package organization

import (
	"github.com/gin-gonic/gin"

	"backend/internal/middleware"
)

// RegisterRoutes mounts the organization endpoints under the given /api/v1 group.
// It is the module's half of the router contract: the composition root passes in
// the shared middleware container and this method composes the per-route chains.
//
// Chain order matches the canonical pattern documented in router/v1.go:
// rate-limit is outermost (throttle before any auth work), then Protected
// (auth → tenant → rbac); mutations additionally carry Idempotency + Audit,
// which sit innermost so they run only after the caller is authenticated and
// authorized. Reads are throttled on the "auth_read" policy, writes on
// "auth_write" (see config.yaml rate policies; an unknown policy fails open).
//
// Note on ordering: /current is registered before /:id. Gin (v1.12) permits a
// static segment and a param segment as siblings, and matches the static one
// first, so "current" is never captured as an {id}.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware) {
	grp := rg.Group("/organizations")

	// Reads — organizations:view.
	grp.GET("/current",
		append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("organizations", "view"), h.GetCurrent)...)...)

	grp.GET("/:id",
		append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("organizations", "view"), h.Get)...)...)

	grp.GET("/:id/summary",
		append([]gin.HandlerFunc{mw.RateLimit("auth_read")},
			append(mw.Protected("organizations", "view"), h.Summary)...)...)

	// Write — organizations:update. Idempotency + Audit wrap the mutation.
	grp.PATCH("/:id",
		append([]gin.HandlerFunc{mw.RateLimit("auth_write")},
			append(mw.Protected("organizations", "update"),
				mw.Idempotency(), mw.Audit(), h.Update)...)...)
}
