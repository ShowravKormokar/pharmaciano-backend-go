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

// ModuleRegistrar is the contract a domain module's HTTP handler satisfies to mount its routes.
type ModuleRegistrar interface {
	RegisterRoutes(rg *gin.RouterGroup, mw *middleware.Middleware)
}

type Deps struct {
	Cfg     *config.Config
	Log     *zap.Logger
	Metrics *telemetry.Metrics
	Health  *telemetry.Health
	MW      *middleware.Middleware

	// Modules are the domain route registrars, mounted under /api/v1 in the order given.
	Modules []ModuleRegistrar
}

// New builds the fully wired Gin engine.
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

// configureProxies makes c.ClientIP() trustworthy.
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
