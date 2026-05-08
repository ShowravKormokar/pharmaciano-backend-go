package middlewares

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"backend/internal/cache"
	"backend/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/ulule/limiter/v3"
	redisStore "github.com/ulule/limiter/v3/drivers/store/redis"
)

func newLimiter(rate limiter.Rate) *limiter.Limiter {
	store, _ := redisStore.NewStoreWithOptions(
		cache.RDB,
		limiter.StoreOptions{
			Prefix: "rl",
		},
	)

	return limiter.New(store, rate)
}
// GENERAL API LIMITER
func GeneralRateLimit(requestsPerHour int64) gin.HandlerFunc {

	limiterInstance := newLimiter(limiter.Rate{
		Period: time.Hour,
		Limit:  requestsPerHour,
	})

	return func(c *gin.Context) {

		key := "ip:" + utils.GetClientIP(c)

		ctx, err := limiterInstance.Get(c, key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rate limiter failed",
			})
			return
		}

		setRateHeaders(c, ctx)

		if ctx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too Many Requests. Please try again later.",
			})
			return
		}

		c.Next()
	}
}
// LOGIN IP LIMITER
// 10 attempts per minute per IP
func LoginRateLimit() gin.HandlerFunc {

	limiterInstance := newLimiter(limiter.Rate{
		Period: time.Minute,
		Limit:  10,
	})

	return func(c *gin.Context) {

		key := "login_ip:" + utils.GetClientIP(c)

		ctx, err := limiterInstance.Get(c, key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rate limiter failed",
			})
			return
		}

		setRateHeaders(c, ctx)

		if ctx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many login attempts from this IP.",
			})
			return
		}

		c.Next()
	}
}
// LOGIN EMAIL LIMITER
// 5 attempts per minute per email
func LoginEmailRateLimit() gin.HandlerFunc {

	limiterInstance := newLimiter(limiter.Rate{
		Period: time.Minute,
		Limit:  5,
	})

	return func(c *gin.Context) {

		email := strings.ToLower(strings.TrimSpace(extractEmailFromBody(c)))

		if email == "" {
			email = "unknown"
		}

		key := "login_email:" + email

		ctx, err := limiterInstance.Get(c, key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rate limiter failed",
			})
			return
		}

		setRateHeaders(c, ctx)

		if ctx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many login attempts for this email.",
			})
			return
		}

		c.Next()
	}
}

// HELPERS
func setRateHeaders(c *gin.Context, ctx limiter.Context) {
	c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", ctx.Limit))
	c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", ctx.Remaining))
	c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", ctx.Reset))
}

func extractEmailFromBody(c *gin.Context) string {

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	var payload map[string]interface{}

	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	email, ok := payload["email"].(string)
	if !ok {
		return ""
	}

	return email
}
