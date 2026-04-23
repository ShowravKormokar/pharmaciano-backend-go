package middlewares

import (
	"fmt"
	"log"
	"net/http"

	"backend/internal/cache"

	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	redisStore "github.com/ulule/limiter/v3/drivers/store/redis"
)

// RateLimitMiddleware applies rate limiting based on IP or user ID
// Default: 100 requests per hour per IP
func RateLimitMiddleware() gin.HandlerFunc {
	store, err := redisStore.NewStoreWithOptions(cache.RedisClient, limiter.StoreOptions{})
	if err != nil {
		log.Fatal("❌ Failed to initialize Redis rate limiter store:", err)
	}

	// Create limiter instance: 100 requests per hour
	limiterInstance := limiter.New(store, limiter.Rate{
		Period: 3600, // 1 hour in seconds
		Limit:  100,  // 100 requests
	})

	return func(c *gin.Context) {
		// Get client IP
		clientIP := c.ClientIP()

		// Get limiter context for this IP
		context, err := limiterInstance.Get(c.Request.Context(), clientIP)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "rate limit error",
			})
			c.Abort()
			return
		}

		// Check if limit reached
		if context.Reached {
			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", context.Limit))
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", context.Reset))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": fmt.Sprintf("Rate limit reset at %d", context.Reset),
			})
			c.Abort()
			return
		}

		// Set rate limit headers
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", context.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", context.Remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", context.Reset))

		c.Next()
	}
}

// RateLimitMiddlewareCustom applies rate limiting with custom configuration
func RateLimitMiddlewareCustom(requestsPerHour int) gin.HandlerFunc {
	store, err := redisStore.NewStoreWithOptions(cache.RedisClient, limiter.StoreOptions{})
	if err != nil {
		log.Fatal("❌ Failed to initialize Redis rate limiter store:", err)
	}
	limiterInstance := limiter.New(store, limiter.Rate{
		Period: 3600,
		Limit:  int64(requestsPerHour),
	})

	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		context, err := limiterInstance.Get(c.Request.Context(), clientIP)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "rate limit error",
			})
			c.Abort()
			return
		}

		if context.Reached {
			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", context.Limit))
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", context.Reset))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": fmt.Sprintf("Rate limit reset at %d", context.Reset),
			})
			c.Abort()
			return
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", context.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", context.Remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", context.Reset))

		c.Next()
	}
}
