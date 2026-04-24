package middlewares

import (
	"fmt"
	"net/http"

	"backend/internal/cache"

	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	redisStore "github.com/ulule/limiter/v3/drivers/store/redis"
)

func RateLimit(requestsPerHour int) gin.HandlerFunc {
	store, _ := redisStore.NewStoreWithOptions(cache.RDB, limiter.StoreOptions{})
	limiterInstance := limiter.New(store, limiter.Rate{
		Period: 3600, // 1 hour
		Limit:  int64(requestsPerHour),
	})

	return func(c *gin.Context) {
		ip := c.ClientIP()
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
