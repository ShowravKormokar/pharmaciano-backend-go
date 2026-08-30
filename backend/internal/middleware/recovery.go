package middleware

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/pkg/response"
)

func (m *Middleware) Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if r == http.ErrAbortHandler {
				panic(r) // let the http server handle its own sentinel
			}

			lg := m.logFor(c)

			if isBrokenPipe(r) {
				lg.Warn("connection lost mid-request", zap.Any("panic", r))
				c.Abort()
				return
			}

			// Surface to the telemetry span (its defer calls span.RecordError on the last c.Error) and log with a full stack.
			_ = c.Error(fmt.Errorf("panic: %v", r)) //nolint:errcheck
			lg.Error("panic recovered",
				zap.Any("panic", r),
				zap.ByteString("stack", debug.Stack()),
			)

			if !c.Writer.Written() {
				rid := appctx.RequestID(c.Request.Context())
				_ = response.Internal(c.Writer, rid)
			}
			c.Abort()
		}()

		c.Next()
	}
}

// isBrokenPipe reports whether a recovered value is a dead-socket write error
// (EPIPE / ECONNRESET), which is a client problem, not a server fault.
func isBrokenPipe(r any) bool {
	err, ok := r.(error)
	if !ok {
		return false
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	var sysErr *os.SyscallError
	if !errors.As(opErr, &sysErr) {
		return false
	}
	msg := strings.ToLower(sysErr.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}

// Recovery converts any panic below it in the chain into a clean 500 envelope
// and a single error-level log line carrying the full stack and the request
// correlation id. It sits just inside RequestID (so panics are correlated) and
// just outside the access log (so it swallows the panic and lets the access
// log still observe the final 500 status rather than an unwind).
//
// Two edge cases are handled the way net/http expects:
//
//   - http.ErrAbortHandler is re-panicked untouched — it is the server's own
//     sentinel for "abort this request", not a bug to be reported.
//   - A broken pipe / connection reset (client vanished mid-write) is logged at
//     warn, not error, and no response is attempted — the socket is already
//     gone, so writing would only produce a second, misleading error.
