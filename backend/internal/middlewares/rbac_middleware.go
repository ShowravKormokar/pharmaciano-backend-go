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
		if role == "" {
			response.Error(c, http.StatusUnauthorized, "unauthorized", nil)
			return
		}
		if !rbac.Enforce(role, module, action) {
			response.Error(c, http.StatusForbidden, "access denied", nil)
			return
		}
		c.Next()
	}
}
