package middlewares

import (
	"backend/internal/rbac"
	"backend/pkg/response"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RBAC(module, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		// DEBUG: [rbac_middleware.go] RBAC
		// fmt.Printf("[rbac_middleware.go] RBAC: role=%s, module=%s, action=%s\n", role, module, action)
		if role == "" {
			response.Error(c, http.StatusUnauthorized, "unauthorized", nil)
			return
		}
		if !rbac.Enforce(role, module, action) {
			// DEBUG: [rbac_middleware.go] RBAC - denied
			// fmt.Printf("[rbac_middleware.go] RBAC: access denied for %s on %s:%s\n", role, module, action)
			response.Error(c, http.StatusForbidden, "access denied", nil)
			return
		}
		// DEBUG: [rbac_middleware.go] RBAC - allowed
		// fmt.Printf("[rbac_middleware.go] RBAC: access granted\n")
		c.Next()
	}
}
