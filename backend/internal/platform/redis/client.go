package redis

import (
	"backend/internal/platform/config"
	"backend/internal/platform/redis"
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type Client struct {
	rdb *redis.Client
	log *zap.Logger
}git add .


// ErrKeyNotFound is returned from Get when the key is absent.
var ErrKeyNotFound = errors.New("redis: key not found")

// New builds a client and pings it. Fails fast on unreachable Redis.
func New(ctx context.Context, cfg config.RedisConfig, log *zap.Logger) (*Client, error) {
	opts := &redis.Options{
		Addr:            fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:        cfg.Password,
		DB:              cfg.DB,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		ConnMaxIdleTime: 10 * time.Minute,
	}
	rdb := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping %s: %w", opts.Addr, err)
	}

	log.Info("redis ready", zap.String("addr", opts.Addr), zap.Int("db", cfg.DB), zap.Int("pool", cfg.PoolSize))

	return &Client{rdb: rdb, log: log}, nil
}

func (c *Client) Underlying() *redis.Client {
	return c.rdb
}

// Ping is a health probe used by /readyz.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.rdb.Ping(ctx).Err()
}

// Close releases the underlying pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Typed helpers used by most modules

// Get returns the string value or ErrKeyNotFound.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	v, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrKeyNotFound
	}
	return v, err
}

// Set with optional TTL(0 = Persist forever)
func (c *Client) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

// SetNX (only if the key doesn't already exist) — building block for locks
// and single-use tokens. Returns (acquired, err).
func (c *Client) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, val, ttl).Result()
}

// Del removes one or more keys and returns the number deleted.
func (c *Client) Del(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	return c.rdb.Del(ctx, keys...).Result()
}

// Exists reports whether at least one of the keys exists.
func (c *Client) Exists(ctx context.Context, keys ...string) (bool, error) {
	n, err := c.rdb.Exists(ctx, keys...).Result()
	return n > 0, err
}

// TTL returns the remaining TTL. -1 = no TTL, -2 = missing.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.rdb.TTL(ctx, key).Result()
}

// Incr atomically increments and returns the new value.
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	return c.rdb.Incr(ctx, key).Result()
}

// IncrWithTTL atomically increments and sets a TTL only on first creation.
// Used by rate limiters and login-attempt counters.
func (c *Client) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	pipe := c.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl) // Expire is *idempotent
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// HSet / HGet / HGetAll for structured session and idempotency payloads.
func (c *Client) HSet(ctx context.Context, key string, values map[string]any, ttl time.Duration) error {
	pipe := c.rdb.TxPipeline()
	pipe.HSet(ctx, key, values)
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	m, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, ErrKeyNotFound
	}
	return m, nil
}

// Scan runs an iterator over keys matching pattern. Non-blocking for large keyspaces because it uses SCAN, not KEYS.
func (c *Client) Scan(ctx context.Context, pattern string, batch int64, fn func(string) error) error {
	var cursor uint64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, pattern, batch).Result()
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := fn(k); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}
