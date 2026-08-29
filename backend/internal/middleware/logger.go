package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
)

var accessLogSkip = map[string]struct{}{
	"/livez":   {},
	"/readyz":  {},
	"/healthz": {},
	"/metrics": {},
}

func (m *Middleware) AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := m.now()

		c.Next()

		route := routeOf(c)
		if _, skip := accessLogSkip[route]; skip {
			return
		}

		ctx := c.Request.Context()
		status := c.Writer.Status()
		latency := m.now().Sub(start)

		fields := []zap.Field{
			zap.String("request_id", appctx.RequestID(ctx)),
			zap.String("method", c.Request.Method),
			zap.String("route", route),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.Int("bytes", c.Writer.Size()),
			zap.String("client_ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
		}

		// Attach identity only when present — unauthenticated routes (login,
		// health) simply omit these keys.
		if appctx.IsAuthenticated(ctx) {
			fields = append(fields,
				zap.String("user_id", appctx.UserID(ctx).String()),
				zap.String("org_id", appctx.OrgID(ctx).String()),
				zap.String("role", appctx.RoleName(ctx)),
			)
			if b := appctx.BranchID(ctx); b != nil {
				fields = append(fields, zap.String("branch_id", b.String()))
			}
		}

		lg := m.logFor(c)
		switch {
		case status >= 500:
			lg.Warn("access", fields...) // the error detail is logged by abortError/Recovery
		default:
			lg.Info("access", fields...)
		}
	}
}
