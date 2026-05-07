package middlewares

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"backend/internal/cache"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	redisStore "github.com/ulule/limiter/v3/drivers/store/redis"
)

// GeneralRateLimit applies a generous limit to normal API routes
func GeneralRateLimit(requestsPerHour int) gin.HandlerFunc {
	store, _ := redisStore.NewStoreWithOptions(cache.RDB, limiter.StoreOptions{})
	limiterInstance := limiter.New(store, limiter.Rate{
		Period: 3600,
		Limit:  int64(requestsPerHour),
	})

	return func(c *gin.Context) {
		ip := utils.GetClientIP(c)
		ctx, err := limiterInstance.Get(c, ip)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", ctx.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", ctx.Remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", ctx.Reset))
		if ctx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// LoginRateLimit - 10 attempts per minute per IP
func LoginRateLimit() gin.HandlerFunc {
	store, _ := redisStore.NewStoreWithOptions(cache.RDB, limiter.StoreOptions{})
	limiterInstance := limiter.New(store, limiter.Rate{
		Period: 60,
		Limit:  10,
	})

	return func(c *gin.Context) {
		ip := utils.GetClientIP(c)
		ctx, err := limiterInstance.Get(c, ip)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", ctx.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", ctx.Remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", ctx.Reset))
		if ctx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// LoginEmailRateLimit limits 5 login attempts per minute per email address.
func LoginEmailRateLimit() gin.HandlerFunc {
	store, _ := redisStore.NewStoreWithOptions(cache.RDB, limiter.StoreOptions{})
	limiterInstance := limiter.New(store, limiter.Rate{
		Period: 60,
		Limit:  5,
	})

	return func(c *gin.Context) {
		email := extractEmailFromBody(c)
		if email == "" {
			// fallback to IP if email can't be extracted
			email = "unknown"
		}
		key := "login_email:" + email
		ctx, err := limiterInstance.Get(c, key)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", ctx.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", ctx.Remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", ctx.Reset))
		if ctx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts for this email, try again later"})
			return
		}
		c.Next()
	}
}

// extractEmailFromBody reads the request body and returns the "email" field.
// It replaces the request body with a new reader so the body can be used again.
func extractEmailFromBody(c *gin.Context) string {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	// Restore body for subsequent handlers
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	var payload map[string]interface{}
	if json.Unmarshal(body, &payload) == nil {
		if email, ok := payload["email"].(string); ok {
			return email
		}
	}
	return ""
}

//Old code for general rate limiter - 100 req/hour per IP
// // Stricter rate limiter for login – 5 attempts per minute per IP
// func LoginRateLimit() gin.HandlerFunc {
// 	store, _ := redisStore.NewStoreWithOptions(cache.RDB, limiter.StoreOptions{})
// 	limiterInstance := limiter.New(store, limiter.Rate{
// 		Period: 60, // 1 minute
// 		Limit:  5,  // 5 attempts
// 	})

//		return func(c *gin.Context) {
//			ip := utils.GetClientIP(c)
//			ctx, err := limiterInstance.Get(c, ip)
//			if err != nil {
//				c.AbortWithStatus(http.StatusInternalServerError)
//				return
//			}
//			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", ctx.Limit))
//			c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", ctx.Remaining))
//			c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", ctx.Reset))
//			if ctx.Reached {
//				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts, try again later"})
//				return
//			}
//			c.Next()
//		}
//	}
