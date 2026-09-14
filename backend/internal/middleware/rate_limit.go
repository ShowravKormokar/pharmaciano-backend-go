package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/redis"
	"backend/pkg/response"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
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
		if securityCriticalRatePolicy(policy) {
			return func(c *gin.Context) {
				m.logFor(c).Error("security rate limiter unavailable; failing closed", zap.String("policy", policy))
				m.abortError(c, errs.New(errs.CodeServiceUnavailable, "authentication protection is temporarily unavailable"))
			}
		}
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
			if securityCriticalRatePolicy(policy) {
				m.logFor(c).Error("security rate limiter unavailable; failing closed",
					zap.String("policy", policy), zap.Error(err))
				m.abortError(c, errs.New(errs.CodeServiceUnavailable, "authentication protection is temporarily unavailable"))
				return
			}
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

func securityCriticalRatePolicy(policy string) bool {
	switch policy {
	case "login_per_ip", "login_per_email", "refresh", "reset", "auth_write":
		return true
	default:
		return false
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

// emailRateLimiterSecret is a per-process HMAC key used to fingerprint the
// email subject of the account/email rate limiter (ADR §16 "Layered rate
// limiting"). It is read lazily from the JWT secret (which is already
// required to be long and secret at startup) and used only to produce a
// 32-character hex digest; the raw email is never written to Redis, so a
// Redis dump cannot be mined for the user list.
//
// We deliberately reuse the JWT secret rather than introducing a new
// configuration knob so the bootstrap surface stays small; the value is only
// read on the rate-limiter code path, not on token verification.
func (m *Middleware) emailLimiterSubject(email string) string {
	if m == nil || m.cfg == nil || m.cfg.JWT.Secret == "" {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(m.cfg.JWT.Secret))
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// RateLimitByEmail is the pre-authentication account/email rate limiter
// (ADR §16). It is distinct from RateLimitByIP because the threat model is
// different:
//
//   - ByIP throttles a single connection — a botnet with N IPs defeats it.
//   - ByEmail throttles a single account — a botnet has to first learn the
//     address, and one typo costs the attacker the bucket.
//
// The subject is HMAC-SHA-256(secret, normalized_email) truncated to 32 hex
// characters so the raw email never lands in Redis. The bucket is shared
// across the whole process (Redis-backed), so distributed workers stay
// consistent. The policy is taken from cfg.RateLimit.Policies[policy]; if
// disabled, missing, or Redis is unavailable, the limiter fails CLOSED —
// login/forgot/reset are security-critical and must never silently accept
// unlimited attempts.
func (m *Middleware) RateLimitByEmail(policy string) gin.HandlerFunc {
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
			m.log.Error("email rate limiter unavailable; failing closed", zap.String("policy", policy))
		}
		return func(c *gin.Context) {
			m.abortError(c, errs.New(errs.CodeServiceUnavailable, "authentication protection is temporarily unavailable"))
		}
	}

	rate := float64(limit) / window.Seconds()
	burst := limit
	ttlMs := window.Milliseconds() * 2

	return func(c *gin.Context) {
		email := m.extractEmail(c)
		if email == "" {
			// No email in the body: the request will fail validation downstream
			// anyway; skip the bucket and let the handler do its job.
			c.Next()
			return
		}
		subject := m.emailLimiterSubject(email)
		if subject == "" {
			// Config is missing the HMAC secret (already gated at startup, but
			// the env might have been mis-rotated). Fail closed.
			m.logFor(c).Error("email rate limiter has no HMAC secret; failing closed",
				zap.String("policy", policy))
			m.abortError(c, errs.New(errs.CodeServiceUnavailable, "authentication protection is temporarily unavailable"))
			return
		}
		key := redis.RateLimitKey(policy, "e:"+subject, "")

		dec, err := m.evalBucket(c, key, rate, burst, ttlMs)
		if err != nil {
			m.logFor(c).Error("email rate limiter unavailable; failing closed",
				zap.String("policy", policy), zap.Error(err))
			m.abortError(c, errs.New(errs.CodeServiceUnavailable, "authentication protection is temporarily unavailable"))
			return
		}

		h := c.Writer.Header()
		h.Set(constants.HeaderRateLimitLimit, strconv.Itoa(dec.limit))
		h.Set(constants.HeaderRateLimitRemain, strconv.Itoa(dec.remaining))
		h.Set(constants.HeaderRateLimitReset, strconv.FormatInt(dec.resetUnix, 10))

		if !dec.allowed {
			m.observeAccountEmailRateLimit(policy)
			m.logFor(c).Warn("email rate limit exceeded",
				zap.String("policy", policy),
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

// extractEmail pulls the "email" field out of a JSON body without forcing a
// full BindJSON pass (the handler does that afterwards with full validation).
// The body is read once, peek-parsed, and the reader is restored so downstream
// handlers see the original stream.
func (m *Middleware) extractEmail(c *gin.Context) string {
	raw, err := readAndRestoreBody(c)
	if err != nil || len(raw) == 0 {
		return ""
	}
	var probe struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(probe.Email))
}
