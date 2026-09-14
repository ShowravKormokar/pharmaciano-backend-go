package middleware

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/pkg/response"
)

// OriginGuard implements the CSRF origin-validation layer prescribed by ADR §12
// ("SameSite=Lax/Strict + Origin validation + CSRF token where required") and
// listed as an open item in the production checklist. The cookie-authenticated
// refresh flow already relies on SameSite; OriginGuard is the defense-in-depth
// that catches the cases SameSite cannot:
//
//   - a same-site=None/legacy deployment, or
//   - the Lax limitation where a top-level cross-site POST (e.g. a form that
//     submits to /auth/password/change) still sends the cookie.
//
// A browser cannot forge the Origin header, so refusing state-changing requests
// whose Origin is not on the CORS allowlist neutralises cross-site forgery even
// if a cookie leaks. OWASP's "Verify Origin with standard headers" flow:
//
//  1. If an Origin header is present, it must match the allowlist.
//  2. Otherwise, if a Referer is present, its scheme://host[:port] must match.
//  3. If neither is present the caller is not a browser (curl, mobile SDK,
//     server-to-server) and carries no ambient credentials, so it is permitted.
//
// Enabled via security.origin_check.enabled (default true). Requests with no
// Origin/Referer are never rejected, so existing non-browser API clients keep
// working unchanged.
func (m *Middleware) OriginGuard() gin.HandlerFunc {
	if m.cfg == nil || !m.cfg.Security.OriginCheck.Enabled {
		// Explicitly disabled or absent — skip entirely (also keeps the middleware
		// a cheap no-op in tests that build a bare Middleware{}).
		return func(c *gin.Context) { c.Next() }
	}

	p := m.buildCORSPolicy()
	if p.allowAll {
		// "*" with credentials is already refused by CORS; without credentials there
		// is no ambient-cookie CSRF surface, so origin checking is moot.
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		if !unsafeMethods[c.Request.Method] {
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		if origin == "" {
			// Fall back to the Referer, converting "https://app.example/path" to its
			// scheme://host[:port] origin so the comparison is apples-to-apples with
			// the Origin header. Unparseable Referers are treated as absent, which is
			// safe: the browser always sends Origin on these requests.
			if referer := c.GetHeader("Referer"); referer != "" {
				if u, uerr := url.Parse(referer); uerr == nil && u.Scheme != "" && u.Host != "" {
					origin = u.Scheme + "://" + u.Host
				}
			}
		}

		// No Origin and no usable Referer: a non-browser client. Permitted.
		if origin == "" {
			c.Next()
			return
		}

		if allowed, _ := p.resolve(origin); allowed {
			c.Next()
			return
		}

		m.abortCrossOrigin(c, origin)
	}
}

// abortCrossOrigin writes a 403 CROSS_ORIGIN_REQUEST envelope in the same shape
// as a rejection from the auth chain, so clients treat it uniformly, and logs
// the attempt as a warning. c.Abort() halts the remaining chain.
func (m *Middleware) abortCrossOrigin(c *gin.Context, origin string) {
	rid := appctx.RequestID(c.Request.Context())
	c.Writer.Header().Set("Vary", "Origin")
	_ = response.Error(c.Writer, rid, http.StatusForbidden, string(errs.CodeCrossOriginRequest),
		"cross-origin state-changing request rejected")

	// Increment the security_denials_total{reason="csrf"} counter so rejected
	// cross-origin attempts are visible in dashboards. Nil-safe wrapper.
	m.observeSecurityDenial("csrf")

	m.logFor(c).Warn("cross-origin state-changing request rejected by origin guard",
		zap.String("origin", origin),
		zap.String("method", c.Request.Method),
		zap.String("path", c.Request.URL.Path))
	c.Abort()
}