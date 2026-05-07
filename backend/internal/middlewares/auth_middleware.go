package middlewares

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/cache"
	"backend/internal/config"
	"backend/internal/errors"
	"backend/internal/models"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
)

func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := extractToken(c)
		if err != nil {
			// DEBUG: [auth_middleware.go] JWTAuth - no token
			// fmt.Printf("[auth_middleware.go] JWTAuth: no token provided\n")
			response.Error(c, http.StatusUnauthorized, "missing or malformed token", nil)
			return
		}

		// DEBUG: [auth_middleware.go] JWTAuth - token extracted
		// fmt.Printf("[auth_middleware.go] JWTAuth: token len=%d\n", len(token))

		claims, err := auth.ValidateToken(token, config.Cfg.JWT.Secret)
		if err != nil {
			// DEBUG: [auth_middleware.go] JWTAuth - invalid token
			// fmt.Printf("[auth_middleware.go] JWTAuth: token validation failed - %v\n", err)
			response.Error(c, http.StatusUnauthorized, "invalid token", nil)
			return
		}

		// Blacklist check
		exists, _ := cache.RDB.Exists(c, cache.TokenBlacklistKey(claims.ID)).Result()
		if exists > 0 {
			// DEBUG: [auth_middleware.go] JWTAuth - blacklisted
			// fmt.Printf("[auth_middleware.go] JWTAuth: token blacklisted (jti=%s)\n", claims.ID)
			response.Error(c, http.StatusUnauthorized, "token revoked", nil)
			return
		}

		// Session validation & hijack detection
		if claims.SessionID != "" {
			data, err := cache.RDB.Get(c, cache.SessionKey(claims.SessionID)).Result()
			if err != nil {
				// DEBUG: [auth_middleware.go] JWTAuth - session expired
				// fmt.Printf("[auth_middleware.go] JWTAuth: session expired or missing (id=%s)\n", claims.SessionID)
				response.Error(c, http.StatusUnauthorized, "session expired", nil)
				c.Abort()
				return
			}

			var sess models.Session
			if json.Unmarshal([]byte(data), &sess) == nil {
				// Hijack detection: fingerprint mismatch
				if sess.DeviceFp != claims.DeviceFingerprint {
					// DEBUG: [auth_middleware.go] JWTAuth - hijack detected
					// fmt.Printf("[auth_middleware.go] JWTAuth: session hijack detected - storedFp=%s tokenFp=%s\n", sess.DeviceFp, claims.DeviceFingerprint)
					cache.RDB.Del(c, cache.SessionKey(claims.SessionID))
					response.Error(c, http.StatusUnauthorized, "session hijack detected", nil)
					c.Abort()
					return
				}

				// IP change warning
				if sess.IP != c.ClientIP() {
					// DEBUG: [auth_middleware.go] JWTAuth - IP changed
					// fmt.Printf("[auth_middleware.go] JWTAuth: IP changed (stored=%s current=%s), setting risk\n", sess.IP, c.ClientIP())
					cache.RDB.Set(c, "risk:"+claims.UserID.String(), "ip_changed", time.Hour)
				}

				// Update last seen
				sess.LastSeen = time.Now()
				sess.IP = c.ClientIP()
				updated, _ := json.Marshal(sess)
				cache.RDB.Set(c, cache.SessionKey(claims.SessionID), updated, time.Until(sess.ExpiresAt))
				// DEBUG: [auth_middleware.go] JWTAuth - session updated
				// fmt.Printf("[auth_middleware.go] JWTAuth: session updated (last_seen=%v)\n", sess.LastSeen)
			} else {
				// DEBUG: [auth_middleware.go] JWTAuth - session unmarshal error
				fmt.Printf("[auth_middleware.go] JWTAuth: failed to unmarshal session data\n")
			}
		}

		c.Set("user_id", claims.UserID.String())
		c.Set("role", claims.Role)
		c.Set("access_token", token)
		c.Next()
	}
}

func extractToken(c *gin.Context) (string, error) {
	cookie, err := c.Cookie("access_token")
	if err == nil && cookie != "" {
		// DEBUG: [auth_middleware.go] extractToken - from cookie
		// fmt.Printf("[auth_middleware.go] extractToken: found access_token cookie\n")
		return cookie, nil
	}
	// DEBUG: [auth_middleware.go] extractToken - not found
	// fmt.Printf("[auth_middleware.go] extractToken: access_token not in cookies\n")
	return "", errors.NewAppError(http.StatusUnauthorized, "missing or malformed token", nil)
}
