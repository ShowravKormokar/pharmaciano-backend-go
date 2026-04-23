package middlewares

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

const TenantContextKey = "tenant_id"

// TenantMiddleware extracts organization/tenant scope from context
func TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract tenant from JWT claims or headers
		tenantID := c.GetString("org_id") // Assumes this is set by auth middleware

		if tenantID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized tenant",
			})
			return
		}

		// Add to context for later use in database queries
		ctx := context.WithValue(c.Request.Context(), TenantContextKey, tenantID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// GetTenantFromContext retrieves tenant ID from context
func GetTenantFromContext(c *gin.Context) string {
	if tenant := c.Request.Context().Value(TenantContextKey); tenant != nil {
		return tenant.(string)
	}
	return ""
}
