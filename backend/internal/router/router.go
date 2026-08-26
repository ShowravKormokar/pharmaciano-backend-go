// Package router assembles the Gin engine: it fixes the middleware order, wires
// the operational endpoints (health, metrics) and mounts the versioned API. It
// depends on the middleware container (behavioural contracts already injected)
// and the platform telemetry helpers — never on domain modules directly, so the
// HTTP wiring compiles and is testable before the modules land.
package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/middleware"
	"backend/internal/platform/config"
	"backend/internal/platform/telemetry"
	"backend/pkg/response"
)

// Deps is the explicit set of collaborators the router needs. Passing them in a
// struct keeps New's signature stable as the system grows.
type Deps struct {
	Cfg     *config.Config
	Log     *zap.Logger
	Metrics *telemetry.Metrics
	Health  *telemetry.Health
	MW      *middleware.Middleware
}

// New builds the fully wired Gin engine.
//
// Middleware order (outermost first):
//
//	telemetry(span+metrics) → request_id → recovery → access-log →
//	security-headers → cors → body-limit → [per-route: rate-limit, auth,
//	tenant, rbac, idempotency, audit] → handler
//
// The telemetry middleware is outermost so its server span and latency/metrics
// cover the entire chain (including a Recovery-handled 500). Everything in
// middleware.Global() then runs inside that span.
func New(d Deps) *gin.Engine {
	if d.Cfg != nil && d.Cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.HandleMethodNotAllowed = true // enables typed 405s below
	configureProxies(engine, d.Cfg)

	// Outermost: distributed-tracing span + Prometheus metrics + in-flight gauge.
	if d.Metrics != nil {
		engine.Use(telemetry.GinMiddleware(d.Metrics))
	}
	// Always-on hardening/observability chain.
	if d.MW != nil {
		engine.Use(d.MW.Global()...)
	}

	// Operational endpoints (health, metrics) — no API versioning, no auth.
	registerOps(engine, d)

	// Versioned application API.
	registerV1(engine, d)

	// Uniform 404 / 405 envelopes so clients never see Gin's plain-text default.
	engine.NoRoute(func(c *gin.Context) {
		rid := appctx.RequestID(c.Request.Context())
		_ = response.Error(c.Writer, rid, http.StatusNotFound, "NOT_FOUND", "the requested resource was not found")
	})
	engine.NoMethod(func(c *gin.Context) {
		rid := appctx.RequestID(c.Request.Context())
		_ = response.Error(c.Writer, rid, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "the method is not allowed for this resource")
	})

	return engine
}

// configureProxies makes c.ClientIP() trustworthy. When the app sits behind a
// known reverse proxy/load balancer we honour X-Forwarded-For, but only from
// private-range hops so a public client can't spoof its address. Otherwise we
// ignore forwarding headers entirely and use the raw socket address.
func configureProxies(engine *gin.Engine, cfg *config.Config) {
	if cfg != nil && cfg.Server.TrustProxy {
		engine.ForwardedByClientIP = true
		_ = engine.SetTrustedProxies([]string{
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"127.0.0.1/32",
			"::1/128",
		})
		return
	}
	engine.ForwardedByClientIP = false
	_ = engine.SetTrustedProxies(nil)
}
