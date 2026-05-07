package middlewares

import (
	"backend/internal/utils"
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func AuditMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body []byte
		if c.Request.Body != nil {
			body, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
		}
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("ip", utils.GetClientIP(c)),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.Time("timestamp", time.Now()),
		}
		if len(body) > 0 {
			var parsed interface{}
			if json.Unmarshal(body, &parsed) == nil {
				fields = append(fields, zap.Any("body", parsed))
			}
		}
		logger.Info("API Request", fields...)
		c.Next()
	}
}
