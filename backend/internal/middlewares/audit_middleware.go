package middlewares

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AuditMiddleware logs requests to audit trail
func AuditMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Capture request body
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		// Log audit trail
		auditLog := map[string]interface{}{
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"ip":         c.ClientIP(),
			"user_id":    c.GetString("user_id"),
			"user_agent": c.Request.UserAgent(),
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}

		if len(bodyBytes) > 0 {
			var body interface{}
			if err := json.Unmarshal(bodyBytes, &body); err == nil {
				auditLog["body"] = body
			}
		}

		logger.Info("API Request", zap.Any("audit", auditLog))
		c.Next()
	}
}
