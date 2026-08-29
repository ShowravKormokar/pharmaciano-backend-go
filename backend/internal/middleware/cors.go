package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// corsPolicy is the per-process, precomputed CORS configuration. Resolving it once keeps the hot path allocation-light.
type corsPolicy struct {
	origins       []string
	allowAll      bool
	methods       string
	headers       string
	exposeHeaders string
	allowCreds    bool
	maxAgeSeconds string
}

func (m *Middleware) CORS() gin.HandlerFunc {
	p := corsPolicy{
		methods:       "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		maxAgeSeconds: "600",
	}
	if m.cfg != nil {
		cc := m.cfg.CORS
		p.allowCreds = cc.AllowCredentials
		p.exposeHeaders = strings.Join(cc.ExposeHeaders, ", ")
		if len(cc.AllowMethods) > 0 {
			p.methods = strings.Join(cc.AllowMethods, ", ")
		}
		if len(cc.AllowHeaders) > 0 {
			p.headers = strings.Join(cc.AllowHeaders, ", ")
		}
		if cc.MaxAge > 0 {
			p.maxAgeSeconds = strconv.Itoa(int(cc.MaxAge.Seconds()))
		}
		for _, o := range cc.AllowOrigins {
			o = strings.TrimSpace(o)
			if o == "*" {
				p.allowAll = true
				continue
			}
			if o != "" {
				p.origins = append(p.origins, o)
			}
		}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		h := c.Writer.Header()

		// The response varies by Origin whether or not we allow it — critical
		// so a shared cache never serves an allowed origin's CORS headers to a
		// different origin.
		h.Add("Vary", "Origin")

		if origin == "" {
			c.Next()
			return
		}

		allowed, allowValue := p.resolve(origin)
		isPreflight := c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != ""

		if isPreflight {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			if !allowed {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			h.Set("Access-Control-Allow-Origin", allowValue)
			if p.allowCreds {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			h.Set("Access-Control-Allow-Methods", p.methods)
			if p.headers != "" {
				h.Set("Access-Control-Allow-Headers", p.headers)
			} else if reqHeaders := c.GetHeader("Access-Control-Request-Headers"); reqHeaders != "" {
				h.Set("Access-Control-Allow-Headers", reqHeaders)
			}
			h.Set("Access-Control-Max-Age", p.maxAgeSeconds)
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		if allowed {
			h.Set("Access-Control-Allow-Origin", allowValue)
			if p.allowCreds {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if p.exposeHeaders != "" {
				h.Set("Access-Control-Expose-Headers", p.exposeHeaders)
			}
		}
		c.Next()
	}
}

// resolve reports whether origin is allowed and the exact value to echo back.
func (p corsPolicy) resolve(origin string) (bool, string) {
	if p.allowAll {
		if p.allowCreds {
			return true, origin // never "*" with credentials
		}
		return true, "*"
	}
	for _, o := range p.origins {
		if o == origin || wildcardMatch(o, origin) {
			return true, origin
		}
	}
	return false, ""
}

// wildcardMatch supports a single "*" in a configured origin, e.g.
// "https://*.pharmaciano.com" matches "https://clinic.pharmaciano.com".
func wildcardMatch(pattern, origin string) bool {
	star := strings.IndexByte(pattern, '*')
	if star < 0 {
		return false
	}
	prefix, suffix := pattern[:star], pattern[star+1:]
	return len(origin) >= len(prefix)+len(suffix) &&
		strings.HasPrefix(origin, prefix) &&
		strings.HasSuffix(origin, suffix)
}

// CORS enforces an origin allowlist. It answers preflights itself and, for
// actual requests, reflects the caller's origin only when it is on the list.
//
// Security notes:
//   - "*" is honoured only when credentials are disabled. The browser forbids
//     wildcard + credentials, and reflecting an arbitrary origin *with*
//     credentials would defeat the same-origin policy — so when credentials are
//     on we echo the specific, allow-listed origin instead and never "*".
//   - Requests with no Origin header are not CORS requests (server-to-server,
//     curl, same-origin navigation) and pass straight through untouched.
//   - A disallowed preflight gets a bare 204 with no CORS headers, so the
//     browser blocks the follow-up request as intended.
