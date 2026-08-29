package middleware

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	"backend/internal/platform/redis"
	"backend/pkg/response"
)

var errMalformedBucketResult = errors.New("middleware: malformed rate-limit script result")

var tokenBucketScript = goredis.NewScript(`
	local key       = KEYS[1]
	local rate      = tonumber(ARGV[1])
	local burst     = tonumber(ARGV[2])
	local now       = tonumber(ARGV[3])
	local requested = tonumber(ARGV[4])
	local ttl       = tonumber(ARGV[5])

	local state  = redis.call('HMGET', key, 'tokens', 'ts')
	local tokens = tonumber(state[1])
	local ts     = tonumber(state[2])
	if tokens == nil then
	tokens = burst
	ts = now
	end

	local delta = math.max(0, now - ts) / 1000.0
	tokens = math.min(burst, tokens + delta * rate)

	local allowed = 0
	if tokens >= requested then
	allowed = 1
	tokens = tokens - requested
	end

	redis.call('HSET', key, 'tokens', tokens, 'ts', now)
	redis.call('PEXPIRE', key, ttl)

	local retry_after = 0
	if allowed == 0 then
	retry_after = math.ceil(((requested - tokens) / rate) * 1000)
	end
	local reset = math.ceil(((burst - tokens) / rate) * 1000)

	return { allowed, math.floor(tokens), retry_after, reset }
`)

// rlDecision is the parsed outcome of one bucket evaluation.
type rlDecision struct {
	allowed   bool
	limit     int   // bucket capacity (X-RateLimit-Limit)
	remaining int   // tokens left (X-RateLimit-Remaining)
	resetUnix int64 // wall-clock second the bucket is full again (X-RateLimit-Reset)
	retryAff  int   // seconds until one token is available (Retry-After), >=1 when blocked
}

func (m *Middleware) RateLimit(policy string) gin.HandlerFunc {
	return m.rateLimit(policy, false)
}

func (m *Middleware) RateLimitByIP(policy string) gin.HandlerFunc {
	return m.rateLimit(policy, true)
}

func (m *Middleware) rateLimit(policy string, byIP bool) gin.HandlerFunc {
	enabled := false
	var (
		limit  int
		window time.Duration
	)
	if m.cfg != nil && m.cfg.RateLimit.Enabled {
		if p, ok := m.cfg.RateLimit.Policies[policy]; ok && p.Limit > 0 && p.Window > 0 {
			enabled = true
			limit = p.Limit
			window = p.Window
		}
	}

	if !enabled || m.redis == nil {
		if m.log != nil {
			m.log.Warn("rate limiter disabled for route family; requests will pass unthrottled",
				zap.String("policy", policy),
				zap.Bool("has_redis", m.redis != nil),
			)
		}
		return func(c *gin.Context) { c.Next() }
	}

	rate := float64(limit) / window.Seconds() // tokens per second
	burst := limit
	ttlMs := window.Milliseconds() * 2

	return func(c *gin.Context) {
		subject := m.rlSubject(c, byIP)
		key := redis.RateLimitKey(policy, subject, "")

		dec, err := m.evalBucket(c, key, rate, burst, ttlMs)
		if err != nil {
			// Fail OPEN. A Redis outage must not lock every user out of the
			// system; we log loudly (so alerting can fire) and let the request
			// through. This is the one middleware that intentionally degrades
			// open rather than closed.
			m.logFor(c).Error("rate limiter unavailable; failing open",
				zap.String("policy", policy),
				zap.String("subject", subject),
				zap.Error(err),
			)
			c.Next()
			return
		}

		// Advertise the quota on every response, allowed or not, so well-behaved
		// clients can self-throttle.
		h := c.Writer.Header()
		h.Set(constants.HeaderRateLimitLimit, strconv.Itoa(dec.limit))
		h.Set(constants.HeaderRateLimitRemain, strconv.Itoa(dec.remaining))
		h.Set(constants.HeaderRateLimitReset, strconv.FormatInt(dec.resetUnix, 10))

		if !dec.allowed {
			m.logFor(c).Warn("rate limit exceeded",
				zap.String("policy", policy),
				zap.String("subject", subject),
				zap.Int("retry_after_s", dec.retryAff),
			)
			rid := appctx.RequestID(c.Request.Context())
			_ = response.TooManyRequests(c.Writer, rid, response.RateLimitInfo{
				Limit:         dec.limit,
				Remaining:     dec.remaining,
				ResetUnix:     dec.resetUnix,
				RetryAfterSec: dec.retryAff,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// rlSubject derives the bucket subject: the user id when authenticated and not forced to IP, otherwise the (proxy-aware) client IP.
func (m *Middleware) rlSubject(c *gin.Context, byIP bool) string {
	ctx := c.Request.Context()
	if !byIP && appctx.IsAuthenticated(ctx) {
		return "u:" + appctx.UserID(ctx).String()
	}
	return "ip:" + c.ClientIP()
}

// evalBucket runs the Lua script and converts its result into an rlDecision.
func (m *Middleware) evalBucket(c *gin.Context, key string, rate float64, burst int, ttlMs int64) (rlDecision, error) {
	nowMs := m.now().UnixMilli()
	res, err := tokenBucketScript.Run(
		c.Request.Context(),
		m.redis.Underlying(),
		[]string{key},
		strconv.FormatFloat(rate, 'f', -1, 64),
		burst,
		nowMs,
		1,
		ttlMs,
	).Result()
	if err != nil {
		return rlDecision{}, err
	}

	arr, ok := res.([]interface{})
	if !ok || len(arr) < 4 {
		return rlDecision{}, errMalformedBucketResult
	}

	allowed := toInt64(arr[0]) == 1
	remaining := int(toInt64(arr[1]))
	retryMs := toInt64(arr[2])
	resetMs := toInt64(arr[3])

	retryAfter := int((retryMs + 999) / 1000) // ceil to whole seconds
	if !allowed && retryAfter < 1 {
		retryAfter = 1
	}
	resetUnix := m.now().Unix() + (resetMs+999)/1000

	return rlDecision{
		allowed:   allowed,
		limit:     burst,
		remaining: remaining,
		resetUnix: resetUnix,
		retryAff:  retryAfter,
	}, nil
}

// toInt64 coerces the numeric types redis can hand back (int64 is the norm for Lua integers, but guard the others defensively).
func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}
