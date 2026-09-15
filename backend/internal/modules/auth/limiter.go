package auth

import (
	"time"

	"backend/internal/common/constants"
)

// LockoutPolicy is the brute-force defence for the login endpoint. It is pure,
// deterministic policy — no clock or storage of its own — so the service can
// drive it with the user row's failed_attempts / locked_until columns and unit
// tests can drive it with a fixed clock. It complements (does not replace) the
// per-IP Redis rate limiter on the /auth routes: the limiter throttles request
// volume from an address, this locks a targeted account after too many failures
// regardless of source IP.
type LockoutPolicy struct {
	// Threshold is the number of consecutive failed attempts that triggers a lock.
	Threshold int
	// Lockout is how long the account stays locked once the threshold is hit.
	Lockout time.Duration
}

// DefaultLockoutPolicy locks an account for 15 minutes after MaxLoginAttempts
// (5) consecutive failures — the shared platform default. A successful login
// resets the counter, so the threshold counts *consecutive* misses.
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		Threshold: constants.MaxLoginAttempts,
		Lockout:   15 * time.Minute,
	}
}

// normalized returns a copy with any non-positive field replaced by its default,
// so a partially-configured policy can never disable the lockout (Threshold 0
// would otherwise lock on the very first attempt, and a 0 duration would lock
// for no time at all).
func (p LockoutPolicy) normalized() LockoutPolicy {
	d := DefaultLockoutPolicy()
	if p.Threshold <= 0 {
		p.Threshold = d.Threshold
	}
	if p.Lockout <= 0 {
		p.Lockout = d.Lockout
	}
	return p
}

// RetryAfter reports how long an account remains locked. It returns 0 when the
// account is not locked (nil lockedUntil, or an expiry already in the past),
// which the caller treats as "may attempt". A positive duration is surfaced to
// the client as a Retry-After hint alongside an ACCOUNT_LOCKED error.
func (p LockoutPolicy) RetryAfter(lockedUntil *time.Time, now time.Time) time.Duration {
	if lockedUntil == nil {
		return 0
	}
	if d := lockedUntil.Sub(now); d > 0 {
		return d
	}
	return 0
}

// NextLock decides the new locked_until value after a failed attempt, given the
// already-incremented consecutive failure count. It returns a non-nil expiry
// (now + Lockout) once the count reaches the threshold, and nil below it. The
// caller persists the returned value (or NULL) on the user row in the same
// update that increments failed_attempts.
func (p LockoutPolicy) NextLock(newFailedCount int, now time.Time) *time.Time {
	pol := p.normalized()
	if newFailedCount < pol.Threshold {
		return nil
	}
	until := now.Add(pol.Lockout)
	return &until
}
