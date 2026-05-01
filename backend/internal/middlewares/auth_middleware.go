package middlewares

import (
	"encoding/json"
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
			response.Error(c, http.StatusUnauthorized, "missing or malformed token", nil)
			return
		}

		claims, err := auth.ValidateToken(token, config.Cfg.JWT.Secret)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "invalid token", nil)
			return
		}

		// Blacklist check
		exists, _ := cache.RDB.Exists(c, cache.TokenBlacklistKey(claims.ID)).Result()
		if exists > 0 {
			response.Error(c, http.StatusUnauthorized, "token revoked", nil)
			return
		}

		// Session validation & hijack detection
		if claims.SessionID != "" {
			data, err := cache.RDB.Get(c, cache.SessionKey(claims.SessionID)).Result()
			if err != nil {
				response.Error(c, http.StatusUnauthorized, "session expired", nil)
				c.Abort()
				return
			}

			var sess models.Session
			if json.Unmarshal([]byte(data), &sess) == nil {
				// Hijack detection: fingerprint mismatch
				if sess.DeviceFp != claims.DeviceFingerprint {
					cache.RDB.Del(c, cache.SessionKey(claims.SessionID))
					response.Error(c, http.StatusUnauthorized, "session hijack detected", nil)
					c.Abort()
					return
				}

				// IP change warning
				if sess.IP != c.ClientIP() {
					cache.RDB.Set(c, "risk:"+claims.UserID.String(), "ip_changed", time.Hour)
				}

				// Update last seen
				sess.LastSeen = time.Now()
				sess.IP = c.ClientIP()
				updated, _ := json.Marshal(sess)
				cache.RDB.Set(c, cache.SessionKey(claims.SessionID), updated, time.Until(sess.ExpiresAt))
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
		return cookie, nil
	}
	return "", errors.NewAppError(http.StatusUnauthorized, "missing or malformed token", nil)
}
