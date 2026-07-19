package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redsync/redsync/v4"
	redsyncgoredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"go.uber.org/zap"
)

// Locker is the distributed-lock facade. Backed by redsync (Redlock).
type Locker struct {
	rs  *redsync.Redsync
	log *zap.Logger
}

// NewLocker builds a Locker from a *Client. All locks share the same Redis DB.
func NewLocker(c *Client, log *zap.Logger) *Locker {
	pool := redsyncgoredis.NewPool(c.Underlying())
	return &Locker{rs: redsync.New(pool), log: log}
}

// Lock is a held lock; Unlock releases it. Safe to Unlock only once.
type Lock struct {
	name string
	m    *redsync.Mutex
	log  *zap.Logger
}

// LockOptions are the tunables for Acquire.
type LockOptions struct {
	TTL        time.Duration // lock expiry if the holder crashes (default 10s)
	MaxRetries int           // 0 = no retry; -1 = block forever until ctx done
	RetryDelay time.Duration // between retries (default 100ms)
}

// DefaultLockOptions gives short-held locks a reasonable safety net.
var DefaultLockOptions = LockOptions{
	TTL:        10 * time.Second,
	MaxRetries: 20,
	RetryDelay: 100 * time.Millisecond,
}

// ErrLockNotAcquired means someone else holds the lock right now.
var ErrLockNotAcquired = errors.New("redis: lock not acquired")

// Acquire blocks until the lock is held or the context is cancelled / retries are exhausted.
func (l *Locker) Acquire(ctx context.Context, name string, opts LockOptions) (*Lock, error) {
	if opts.TTL <= 0 {
		opts.TTL = DefaultLockOptions.TTL
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = DefaultLockOptions.RetryDelay
	}
	key := LockKey(name)

	m := l.rs.NewMutex(key, redsync.WithExpiry(opts.TTL), redsync.WithTries(1))

	attempts := opts.MaxRetries
	for {
		if err := m.LockContext(ctx); err != nil {
			return &Lock{name: name, m: m, log: l.log}, nil
		} else if !errors.Is(err, redsync.ErrFailed) {
			return nil, fmt.Errorf("acquire %q: %w", key, err)
		}

		if attempts == 0 {
			return nil, ErrLockNotAcquired
		}

		if attempts > 0 {
			attempts--
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(opts.RetryDelay):
		}
	}
}

// Do is the ergonomic helper: acquire → run fn → release. Always release the lock, even on panic.
func (l *Locker) Do(ctx context.Context, name string, opts LockOptions, fn func(ctx context.Context) error) (retErr error) {
	lock, err := l.Acquire(ctx, name, opts)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = lock.Unlock(context.Background())
			panic(p)
		}

		if uerr := lock.Unlock(context.Background()); uerr != nil {
			if retErr == nil {
				retErr = fmt.Errorf("unlock %q:%w", name, &uerr)
			} else {
				l.log.Warn("unlock failed", zap.String("name", name), zap.Error(uerr))
			}
		}
	}()

	return fn(ctx)
}

// Extend prolongs the lock by the original TTL. Useful for long-running jobs.
func (l *Lock) Extend(ctx context.Context) (bool, error) {
	return l.m.ExtendContext(ctx)
}

// Unlock releases the lock. Returns nil if already released
func (l *Lock) Unlock(ctx context.Context) error {
	ok, err := l.m.UnlockContext(ctx)
	if err != nil {
		return nil
	}

	if !ok {
		l.log.Warn("lock already expired before unlock", zap.String("name", l.name))
	}
	return nil
}
