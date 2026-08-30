package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	uuidx "backend/internal/platform/uuid"
)

func (m *Middleware) RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := sanitizeRequestID(c.GetHeader(constants.HeaderRequestID))
		if rid == "" {
			rid = uuidx.NewV7String()
		}

		// Echo back straight away — recovery/access-log read it from here.
		c.Header(constants.HeaderRequestID, rid)

		ctx := c.Request.Context()
		ctx = appctx.WithRequestID(ctx, rid)
		ctx = appctx.WithStartTime(ctx, m.now())
		ctx = appctx.WithClientIP(ctx, c.ClientIP())
		ctx = appctx.WithUserAgent(ctx, c.Request.UserAgent())
		if loc := primaryLocale(c.GetHeader("Accept-Language")); loc != "" {
			ctx = appctx.WithLocale(ctx, loc)
		}
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// requestIDMaxLen bounds an adopted id so a hostile client can't blow up log storage or headers with a multi-kilobyte value.
const requestIDMaxLen = 128

func sanitizeRequestID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 8 || len(s) > requestIDMaxLen {
		return ""
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= 'A' && ch <= 'Z',
			ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == ':':
		default:
			return ""
		}
	}
	return s
}

// primaryLocale extracts the first language tag from an Accept-Language header ("en-US,en;q=0.9,bn;q=0.8" → "en-US").
func primaryLocale(h string) string {
	if h == "" {
		return ""
	}
	if i := strings.IndexByte(h, ','); i >= 0 {
		h = h[:i]
	}
	if i := strings.IndexByte(h, ';'); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimSpace(h)
	if len(h) == 0 || len(h) > 35 { // RFC 5646 tags are short
		return ""
	}
	for i := 0; i < len(h); i++ {
		ch := h[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '*') {
			return ""
		}
	}
	return h
}

// RequestID establishes the base request context that every other middleware,
// log line and error envelope keys off of. It is the outermost of our own
// middleware so a correlation id exists before anything can fail.
//
// It does four things:
//
//  1. Adopts a caller-supplied X-Request-ID when it is well-formed, otherwise
//     mints a time-ordered UUIDv7 (sortable in logs). Adopting the client's id
//     lets a frontend/gateway stitch its traces to ours — but only after strict
//     validation, so the header can never be used to forge or inject log lines.
//  2. Stores request-id + start-time on the context (start-time powers the
//     access log's latency and appctx.Elapsed).
//  3. Captures client ip, user-agent and locale once, so unauthenticated paths
//     (login, health) and the audit log all have them without re-parsing.
//  4. Echoes the request-id back on the response immediately, so it is present
//     even if a later middleware panics and the recovery handler takes over.
