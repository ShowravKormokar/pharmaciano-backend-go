package router

import "github.com/gin-gonic/gin"

// registerOps mounts the operational endpoints used by orchestrators and
// scrapers. They live at the root (no /api/v1 prefix) and outside the API's
// auth chain, but still run inside Global() — so they get a request id, panic
// recovery and security headers while being excluded from access logging by the
// AccessLog skip-list.
//
// The health *checkers* (postgres, redis, …) are registered on d.Health by the
// composition root (cmd/api/main.go), which is the only place that owns the live
// pool/client handles. This keeps the router package decoupled from the platform
// db/redis types and trivially testable with a bare telemetry.Health.
//
// Endpoints:
//
//	GET /livez   — liveness: 200 while the process is up. Never touches
//	               Postgres/Redis, so a dependency outage yields "not ready"
//	               (503 on /readyz) instead of a container restart loop.
//	GET /healthz — alias of liveness, for platforms that probe the conventional
//	               path.
//	GET /readyz  — readiness: runs every registered checker (briefly cached) and
//	               returns 200 only when all pass, else 503.
//	GET /metrics — Prometheus exposition, optionally Bearer-guarded. Mounted only
//	               when telemetry.metrics.enabled and a Metrics registry exists.
func registerOps(engine *gin.Engine, d Deps) {
	if d.Health != nil {
		live := gin.WrapF(d.Health.LivenessHandler())
		ready := gin.WrapF(d.Health.ReadinessHandler())

		engine.GET("/livez", live)
		engine.GET("/healthz", live)
		engine.GET("/readyz", ready)
	}

	if d.Metrics == nil || d.Cfg == nil || !d.Cfg.Telemetry.Metrics.Enabled {
		return
	}

	path := d.Cfg.Telemetry.Metrics.Path
	if path == "" {
		path = "/metrics"
	}
	// Handler applies the optional Bearer-token guard itself; when the token is
	// empty it serves unauthenticated (appropriate only behind a trusted network).
	engine.GET(path, gin.WrapH(d.Metrics.Handler(d.Cfg.Telemetry.Metrics.AuthToken)))
}
