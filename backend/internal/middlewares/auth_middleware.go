package middlewares

import (
	"net/http"

	"backend/internal/auth"
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/errors"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
)

func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := extractToken(c)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "missing or malformed token", nil)
			return
		}

		claims, err := auth.ValidateToken(token, config.Cfg.JWT.Secret)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "invalid token", nil)
			return
		}

		// Check blacklist
		exists, _ := cache.RDB.Exists(c, cache.TokenBlacklistKey(claims.ID)).Result()
		if exists > 0 {
			response.Error(c, http.StatusUnauthorized, "token revoked", nil)
			return
		}

		c.Set("user_id", claims.UserID.String())
		c.Set("role", claims.Role)
		c.Set("access_token", token) // for logout
		c.Next()
	}
}

func extractToken(c *gin.Context) (string, error) {
	//  Try Authorization header first
	// header := c.GetHeader("Authorization")
	// if header != "" {
	// 	parts := strings.SplitN(header, " ", 2)
	// 	if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
	// 		return parts[1], nil
	// 	}
	// }

	// Fallback to cookie
	cookie, err := c.Cookie("access_token")
	if err == nil && cookie != "" {
		return cookie, nil
	}

	return "", errors.NewAppError(http.StatusUnauthorized, "missing or malformed token", nil)
}
