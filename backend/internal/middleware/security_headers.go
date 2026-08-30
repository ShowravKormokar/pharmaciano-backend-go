package middleware

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Default security-header values, used when config leaves a field blank so the system is safe-by-default even under a partial config.
const (
	defaultXFrameOptions       = "DENY"
	defaultXContentTypeOptions = "nosniff"
	defaultReferrerPolicy      = "no-referrer"
	defaultCSP                 = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	defaultPermissionsPolicy   = "geolocation=(), microphone=(), camera=(), payment=(), usb=(), interest-cohort=()"
)

func (m *Middleware) SecurityHeaders() gin.HandlerFunc {
	// Resolve values once at construction, not per-request.
	var (
		hstsValue     string
		csp           = defaultCSP
		xFrame        = defaultXFrameOptions
		xContentType  = defaultXContentTypeOptions
		referrer      = defaultReferrerPolicy
		permissions   = defaultPermissionsPolicy
		enableHSTS    bool
	)

	if m.cfg != nil {
		sec := m.cfg.Security
		if sec.CSP != "" {
			csp = sec.CSP
		}
		if sec.XFrameOptions != "" {
			xFrame = sec.XFrameOptions
		}
		if sec.XContentTypeOptions != "" {
			xContentType = sec.XContentTypeOptions
		}
		if sec.ReferrerPolicy != "" {
			referrer = sec.ReferrerPolicy
		}
		if sec.HSTS.Enabled {
			enableHSTS = true
			hstsValue = buildHSTS(sec.HSTS.MaxAge, sec.HSTS.IncludeSubdomains, sec.HSTS.Preload)
		}
	}

	return func(c *gin.Context) {
		h := c.Writer.Header()
		if enableHSTS {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", xContentType)
		h.Set("X-Frame-Options", xFrame)
		h.Set("Referrer-Policy", referrer)
		h.Set("X-XSS-Protection", "0")
		h.Set("Permissions-Policy", permissions)
		h.Set("X-Permitted-Cross-Domain-Policies", "none")

		c.Next()
	}
}

// buildHSTS assembles the Strict-Transport-Security value. maxAge <= 0 falls
// back to one year, the value the preload list requires.
func buildHSTS(maxAge int, includeSub, preload bool) string {
	if maxAge <= 0 {
		maxAge = 31536000 // 1 year
	}
	var b strings.Builder
	b.WriteString("max-age=")
	b.WriteString(strconv.Itoa(maxAge))
	if includeSub {
		b.WriteString("; includeSubDomains")
	}
	if preload {
		b.WriteString("; preload")
	}
	return b.String()
}

// SecurityHeaders writes the browser hardening headers on every response. They
// are set before c.Next so they are present even on early aborts (401/403/429).
// Values come from config where provided, falling back to strict defaults.
//
// Rationale for each header:
//   - Strict-Transport-Security: force HTTPS for a year incl. subdomains; only
//     emitted when enabled (browsers ignore it over plain HTTP, but emitting it
//     unconditionally lets a TLS-terminating proxy front the app).
//   - Content-Security-Policy: this is a JSON API — the default denies all
//     resource loading and framing, neutralising XSS/clickjacking on any HTML
//     an attacker might coax a browser into rendering from a response.
//   - X-Content-Type-Options=nosniff: stop MIME sniffing of our JSON as HTML/JS.
//   - X-Frame-Options=DENY: legacy clickjacking defence alongside CSP.
//   - Referrer-Policy=no-referrer: never leak our URLs (which can carry ids).
//   - X-XSS-Protection=0: explicitly disable the buggy legacy auditor.
//   - Permissions-Policy: switch off powerful browser features we never use.