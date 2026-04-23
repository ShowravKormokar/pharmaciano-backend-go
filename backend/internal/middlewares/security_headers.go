package middlewares

import "github.com/gin-gonic/gin"

// SecurityHeadersMiddleware sets HTTP security headers
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// XSS Protection
		c.Header("X-XSS-Protection", "1; mode=block")

		// Clickjacking Protection
		c.Header("X-Frame-Options", "DENY")

		// MIME Type Sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Referrer Policy
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy
		c.Header("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self' 'unsafe-inline'")

		// Strict Transport Security
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Permissions Policy
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		c.Next()
	}
}
