package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/pkg/response"
)

func (m *Middleware) Timeout(d time.Duration) gin.HandlerFunc {
	if d <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		if ctx.Err() == context.DeadlineExceeded && !c.Writer.Written() {
			rid := appctx.RequestID(c.Request.Context())
			_ = response.Error(c.Writer, rid, http.StatusGatewayTimeout,
				string(errs.CodeTimeout), "request timed out")
			c.Abort()
		}
	}
}

func (m *Middleware) BodyLimit() gin.HandlerFunc {
	var maxBytes int64
	if m.cfg != nil {
		maxBytes = int64(m.cfg.Server.BodyLimitMB) * 1024 * 1024
	}
	return func(c *gin.Context) {
		if maxBytes > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// Timeout attaches a hard deadline to the request context. Every context-aware
// operation downstream (pgx queries, redis calls, outbound HTTP) observes the
// deadline and returns context.DeadlineExceeded, which the error taxonomy maps
// to 504. If the deadline fires and the handler returned without writing
// anything, this middleware finalises the response as a 504 itself.
//
// This is the "context timeout" the architecture calls for: it is race-free
// (the handler runs on the same goroutine, so there is no concurrent writer)
// and relies on cooperative cancellation. A handler that ignores its context
// and blocks on CPU work will not be force-killed — that is a handler bug, and
// the right fix is to make the handler context-aware, not to intercept its
// writer here and risk a torn/duplicated response.
//
// d <= 0 disables the timeout (returns a pass-through), which is handy for
// long-poll / streaming routes that opt out.
