package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/google/uuid"

	"backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
)

// SessionCache is the ADR §17 Redis fast path for the Authenticate hot path.
// It caches the security-relevant projection of a session row, keyed on
// (session_id, security_generation): when a session is revoked its
// security_generation counter advances, the new lookup writes under a new
// key, and the stale entry becomes unreachable without any Redis-side
// invalidation. The cache is strictly a performance optimisation — the
// caller always falls back to PostgreSQL primary on miss/error and treats
// any stale or malformed cached value as a miss.
//
// Cached projection is deliberately minimal: the fields Authenticate reads.
// It does NOT include sensitive data such as device_fp or raw IP beyond
// what the auth middleware already accepts.
type SessionCache struct {
	rdb     *redis.Client
	ttl     time.Duration
	log     *zap.Logger
	metrics *telemetry.Metrics
}

// NewSessionCache wires the cache. ttl <= 0 picks the safe default
// (60 seconds — short enough that a revocation's Redis eviction failure leaves
// only a tiny stale-projection window, long enough to absorb login spikes).
// metrics may be nil; lookups/store/invalidate no-op the counters in that case.
func NewSessionCache(rdb *redis.Client, ttl time.Duration, metrics *telemetry.Metrics, log *zap.Logger) *SessionCache {
	if log == nil {
		log = zap.NewNop()
	}
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &SessionCache{rdb: rdb, ttl: ttl, log: log, metrics: metrics}
}

// sessionCacheEntry is the cached projection. JSON-tagged because the value
// lives in Redis as opaque text.
type sessionCacheEntry struct {
	UserID             uuid.UUID   `json:"uid"`
	OrganizationID     uuid.UUID   `json:"org"`
	BranchID           *uuid.UUID  `json:"bid,omitempty"`
	BranchIDs          []uuid.UUID `json:"bids,omitempty"`
	AuthzVersion       int64       `json:"av"`
	CanLogin           bool        `json:"cl"`
	SecurityGeneration int64       `json:"g"`
	ExpiresAt          time.Time   `json:"exp"`
}

// ErrSessionCacheMiss is returned when the cached projection is absent for
// the current (id, generation). The caller falls back to the primary DB.
var ErrSessionCacheMiss = errors.New("auth: session cache miss")

// Lookup returns the cached projection for (id, generation) or
// ErrSessionCacheMiss. A Redis error is logged and reported as a miss so
// the caller falls back to PostgreSQL — never to a denial. A malformed
// cached value is also a miss, with the corrupt entry deleted and the
// authz_cache_error_total counter incremented by the caller (via the
// returned bool flag).
func (c *SessionCache) Lookup(ctx context.Context, id uuid.UUID, generation int64) (entry sessionCacheEntry, ok bool, err error) {
	if c == nil || c.rdb == nil {
		c.observeCache("skip")
		return sessionCacheEntry{}, false, ErrSessionCacheMiss
	}
	key := redis.SessionCacheKey(id, generation)
	raw, err := c.rdb.Get(ctx, key)
	if err != nil {
		if errors.Is(err, redis.ErrKeyNotFound) {
			c.observeCache("miss")
			return sessionCacheEntry{}, false, ErrSessionCacheMiss
		}
		c.observeCache("error")
		c.log.Warn("session cache lookup failed; falling back to primary",
			zap.String("session_id", id.String()),
			zap.Error(err))
		return sessionCacheEntry{}, false, ErrSessionCacheMiss
	}
	var e sessionCacheEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		// Malformed JSON is a corruption event: log + delete + miss.
		c.observeCache("corrupt")
		c.log.Warn("session cache returned malformed entry; deleting and falling back",
			zap.String("session_id", id.String()),
			zap.Error(err))
		c.invalidate(ctx, id, generation)
		return sessionCacheEntry{}, false, ErrSessionCacheMiss
	}
	c.observeCache("hit")
	return e, true, nil
}

// Store writes the cached projection. Errors are logged and swallowed —
// a Redis outage must never break a real login.
func (c *SessionCache) Store(ctx context.Context, id uuid.UUID, entry sessionCacheEntry) {
	if c == nil || c.rdb == nil {
		return
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		c.observeStore("error")
		c.log.Warn("session cache marshal failed", zap.Error(err))
		return
	}
	if err := c.rdb.Set(ctx, redis.SessionCacheKey(id, entry.SecurityGeneration), string(payload), c.ttl); err != nil {
		c.observeStore("error")
		c.log.Warn("session cache write failed",
			zap.String("session_id", id.String()),
			zap.Error(err))
		return
	}
	c.observeStore("stored")
}

// InvalidateAll best-effort deletes every generation of a session's cache
// projection. Used when a session is revoked and we want to make the
// stale entries disappear immediately rather than wait for TTL.
//
// We can't enumerate keys by prefix cheaply, so a single SCAN pass is run
// only on explicit invalidation (logout, force-revoke). The hot path
// relies on generation advance + TTL instead.
func (c *SessionCache) InvalidateAll(ctx context.Context, id uuid.UUID) {
	if c == nil || c.rdb == nil {
		return
	}
	pattern := redis.SessionCacheKey(id, 0)
	// Strip the trailing ":g0" so SCAN matches every generation.
	if len(pattern) >= 3 {
		pattern = pattern[:len(pattern)-3] + "*"
	}
	err := c.rdb.Scan(ctx, pattern, 64, func(key string) error {
		_, e := c.rdb.Del(ctx, key)
		return e
	})
	if err != nil {
		c.observeInvalidate("security_generation_bump")
		c.log.Warn("session cache invalidate failed",
			zap.String("session_id", id.String()),
			zap.Error(err))
		return
	}
	c.observeInvalidate("security_generation_bump")
}

func (c *SessionCache) invalidate(ctx context.Context, id uuid.UUID, generation int64) {
	if c == nil || c.rdb == nil {
		return
	}
	if _, err := c.rdb.Del(ctx, redis.SessionCacheKey(id, generation)); err != nil {
		c.observeInvalidate("security_generation_bump")
		c.log.Warn("session cache delete failed",
			zap.String("session_id", id.String()),
			zap.Error(err))
		return
	}
	c.observeInvalidate("security_generation_bump")
}

// observeCache / observeStore / observeInvalidate are nil-safe forwards to the
// shared Prometheus registry. They keep the cache itself free of nil-checks at
// every call site.
func (c *SessionCache) observeCache(outcome string) {
	if c == nil || c.metrics == nil {
		return
	}
	c.metrics.ObserveSessionCache(outcome)
}
func (c *SessionCache) observeStore(outcome string) {
	if c == nil || c.metrics == nil {
		return
	}
	c.metrics.ObserveSessionCacheStore(outcome)
}
func (c *SessionCache) observeInvalidate(reason string) {
	if c == nil || c.metrics == nil {
		return
	}
	c.metrics.ObserveSessionCacheInvalidate(reason)
}
