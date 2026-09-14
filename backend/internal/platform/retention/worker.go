// Package retention runs a background worker that deletes expired auth-related rows.
// It is intentionally narrow — the audit_logs table is RANGE-partitioned with a
// write-protect archive trigger, so its lifecycle is managed outside this package
// (a future partitioned-table reaper will DROP old partitions instead of issuing
// row DELETES).
package retention

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Config controls when and how much the worker deletes per sweep.
// Each grace period is added on top of the row's natural expiry — for example
// a session whose expires_at is 10h old with SessionsGrace=24h is kept; one
// whose expires_at is 25h old is removed. The extra delay gives operators a
// window to inspect recently-expired data during an incident.
type Config struct {
	// Enabled short-circuits the worker; callers check this before starting.
	Enabled bool
	// Interval is how often the worker wakes up to sweep. 0 disables sweeps
	// while running (a single sweep can still be triggered via Sweep).
	Interval time.Duration
	// BatchSize caps rows deleted per table per sweep. 0 falls back to 500.
	BatchSize int
	// SessionsGrace is the extra retention past sessions.expires_at.
	SessionsGrace time.Duration
	// RefreshTokensGrace is the extra retention past refresh_tokens.expires_at
	// and the extra retention for already-revoked / already-used tokens.
	RefreshTokensGrace time.Duration
	// PasswordResetsGrace is the extra retention past password_resets.expires_at.
	PasswordResetsGrace time.Duration
	// MFAChallengesGrace is the extra retention past mfa_challenges.expires_at.
	MFAChallengesGrace time.Duration

	// password_history is deliberately NOT swept here: it is already bounded by
	// the auth repo's inline trim (InsertPasswordHistory deletes beyond its size
	// cap), and a grace-based sweep would risk removing hashes the reuse-rejection
	// check still needs.
}

// Worker drives the periodic retention sweep.
type Worker struct {
	pool *pgxpool.Pool
	cfg  Config
	log  *zap.Logger
}

// New builds a worker. pool must be non-nil; the pool is not closed by the
// worker. log may be nil (no logging).
func New(pool *pgxpool.Pool, cfg Config, log *zap.Logger) *Worker {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{pool: pool, cfg: cfg, log: log}
}

// Run blocks until ctx is cancelled, running a sweep every cfg.Interval and
// once eagerly at start. It returns ctx.Err() and logs the reason. Callers
// should start it in a goroutine and stop it by cancelling ctx (main.go's
// signal.NotifyContext does exactly this during shutdown).
func (w *Worker) Run(ctx context.Context) error {
	if !w.cfg.Enabled {
		w.log.Info("retention worker disabled; skipping")
		<-ctx.Done()
		return ctx.Err()
	}
	if w.pool == nil {
		w.log.Warn("retention worker: pool is nil; no-op")
		<-ctx.Done()
		return ctx.Err()
	}

	// Eager first sweep so a long interval (e.g. 1h) doesn't leave stale rows
	// for a long time after a deploy.
	w.sweepOnce(ctx)

	if w.cfg.Interval <= 0 {
		w.log.Info("retention worker: interval is zero; running one sweep only")
		<-ctx.Done()
		return ctx.Err()
	}

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("retention worker stopped")
			return ctx.Err()
		case <-ticker.C:
			w.sweepOnce(ctx)
		}
	}
}

// sweepOnce issues the per-table DELETES and logs a summary. Each table is
// independent — failure on one is logged and does not prevent the others from
// running.
func (w *Worker) sweepOnce(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	for _, tbl := range []struct {
		name string
		fn   func(context.Context) (int64, error)
	}{
		{"sessions", w.sweepSessions},
		{"refresh_tokens", w.sweepRefreshTokens},
		{"password_resets", w.sweepPasswordResets},
		{"mfa_challenges", w.sweepMFAChallenges},
	} {
		n, err := tbl.fn(sctx)
		if err != nil {
			// Sweep context timeout is worth a warning; other errors are still
			// actionable so we leave them as error level.
			if sctx.Err() == context.DeadlineExceeded {
				w.log.Warn("retention sweep deadline exceeded", zap.String("table", tbl.name), zap.Error(err))
			} else {
				w.log.Error("retention sweep failed", zap.String("table", tbl.name), zap.Error(err))
			}
			continue
		}
		if n > 0 {
			w.log.Info("retention sweep deleted expired rows",
				zap.String("table", tbl.name), zap.Int64("count", n))
		}
	}
}

// Sweep runs one synchronous pass and returns the total deleted rows. It is
// idempotent. Useful for ad-hoc invocations or tests; Run calls it internally.
func (w *Worker) Sweep(ctx context.Context) (int64, error) {
	if w.pool == nil {
		return 0, nil
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var total int64
	for _, fn := range []func(context.Context) (int64, error){
		w.sweepSessions,
		w.sweepRefreshTokens,
		w.sweepPasswordResets,
		w.sweepMFAChallenges,
	} {
		n, err := fn(sctx)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// sweepSessions deletes sessions whose expires_at+SessionsGrace is in the past.
func (w *Worker) sweepSessions(ctx context.Context) (int64, error) {
	const q = `DELETE FROM sessions
		WHERE expires_at + $2::interval < now()
		AND id IN (
			SELECT id FROM sessions
			WHERE expires_at + $2::interval < now()
			ORDER BY expires_at
			LIMIT $1
		)`
	tag, err := w.pool.Exec(ctx, q, w.cfg.BatchSize, durationPG(w.cfg.SessionsGrace))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// sweepRefreshTokens deletes refresh tokens that are dead and past the grace:
//   - expired (expires_at + grace < now), or
//   - already consumed by rotation (used_at set), or
//   - explicitly revoked (revoked_at set), or
//   - superseded by a successor (replaced_by set)
//
// The superseded cases are aged from updated_at (when the rotation/revoke
// happened) so recently-superseded tokens are still reclaimable on a rollback.
func (w *Worker) sweepRefreshTokens(ctx context.Context) (int64, error) {
	const q = `DELETE FROM refresh_tokens
		WHERE (
			  expires_at + $2::interval < now()
		   OR (used_at IS NOT NULL AND updated_at + $2::interval < now())
		   OR (revoked_at IS NOT NULL AND updated_at + $2::interval < now())
		   OR (replaced_by IS NOT NULL AND updated_at + $2::interval < now())
		)
		AND id IN (
			SELECT id FROM refresh_tokens
			WHERE (
				  expires_at + $2::interval < now()
			   OR (used_at IS NOT NULL AND updated_at + $2::interval < now())
			   OR (revoked_at IS NOT NULL AND updated_at + $2::interval < now())
			   OR (replaced_by IS NOT NULL AND updated_at + $2::interval < now())
			)
			ORDER BY expires_at
			LIMIT $1
		)`
	tag, err := w.pool.Exec(ctx, q, w.cfg.BatchSize, durationPG(w.cfg.RefreshTokensGrace))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// sweepPasswordResets deletes password reset tokens whose expires_at is past
// the grace, or whose used_at indicates they were already consumed and are
// past the grace from updated_at.
func (w *Worker) sweepPasswordResets(ctx context.Context) (int64, error) {
	const q = `DELETE FROM password_resets
		WHERE (
			  expires_at + $2::interval < now()
		   OR (used_at IS NOT NULL AND updated_at + $2::interval < now())
		)
		AND id IN (
			SELECT id FROM password_resets
			WHERE (
				  expires_at + $2::interval < now()
			   OR (used_at IS NOT NULL AND updated_at + $2::interval < now())
			)
			ORDER BY expires_at
			LIMIT $1
		)`
	tag, err := w.pool.Exec(ctx, q, w.cfg.BatchSize, durationPG(w.cfg.PasswordResetsGrace))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// sweepMFAChallenges deletes MFA challenge tokens whose expires_at is past the
// grace, or already used (used_at set) and past the grace from updated_at.
func (w *Worker) sweepMFAChallenges(ctx context.Context) (int64, error) {
	const q = `DELETE FROM mfa_challenges
		WHERE (
			  expires_at + $2::interval < now()
		   OR (used_at IS NOT NULL AND updated_at + $2::interval < now())
		)
		AND id IN (
			SELECT id FROM mfa_challenges
			WHERE (
				  expires_at + $2::interval < now()
			   OR (used_at IS NOT NULL AND updated_at + $2::interval < now())
			)
			ORDER BY expires_at
			LIMIT $1
		)`
	tag, err := w.pool.Exec(ctx, q, w.cfg.BatchSize, durationPG(w.cfg.MFAChallengesGrace))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// durationPG converts d to a Postgres interval literal. go's Duration.String()
// ("24h0m0s") is NOT understood by Postgres, so we render whole seconds, which
// every interval-decode path accepts ("90000 seconds" → 25:00:00). Zero becomes
// "0 seconds" so the WHERE clause needs no conditional SQL.
func durationPG(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := d.Seconds()
	// Whole seconds are the common case; render them as an integer to avoid
	// float drift in the SQL text. Fractional seconds only appear if someone
	// configures a sub-second grace.
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(secs), 10) + " seconds"
	}
	return strconv.FormatFloat(secs, 'f', -1, 64) + " seconds"
}
