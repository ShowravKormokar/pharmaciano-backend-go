package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// TxOptions are the knobs callers may set. Defaults are ReadCommitted, RW.
type TxOptions struct {
	Isolation            pgx.TxIsoLevel       // Serializable, RepeatableRead, ReadCommitted (default)
	AccessMode           pgx.TxAccessMode     // pgx.ReadOnly / pgx.ReadWrite
	DeferrableMode       pgx.TxDeferrableMode // RetryOnSerialization retries the fn up to MaxRetries times on 40001.
	RetryOnSerialization bool
	MaxRetries           int
	Timeout              time.Duration
}

// DefaultTxOptions is the safe baseline: RC, RW, no retry.
var DefaultTxOptions = TxOptions{
	Isolation:  pgx.ReadCommitted,
	AccessMode: pgx.ReadWrite,
	Timeout:    30 * time.Second,
}

// WithTx runs fn inside a transaction. On non-nil error, the tx is rolled
// back; on nil error, it commits. Nested calls (fn calling WithTx again on
// the same ctx) use SAVEPOINTs instead of opening a second real tx.
func (d *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return d.WithTxOptions(ctx, DefaultTxOptions, fn)
}

// WithTxReadOnly runs fn in a read-only tx (helpful for consistency snapshots).
func (d *DB) WithTxReadOnly(ctx context.Context, fn func(ctx context.Context) error) error {
	opts := DefaultTxOptions
	opts.AccessMode = pgx.ReadOnly
	return d.WithTxOptions(ctx, opts, fn)
}

// WithTxSerializable runs fn under SERIALIZABLE with automatic retry on 40001.
// Use for ledger posts, invoice numbering, stock decrement.
func (d *DB) WithTxSerialization(ctx context.Context, fn func(ctx context.Context) error) error {
	opts := DefaultTxOptions
	opts.Isolation = pgx.Serializable
	opts.RetryOnSerialization = true
	opts.MaxRetries = 3
	return d.WithTxOptions(ctx, opts, fn)
}

// WithTxOptions is the full-control variant.
func (d *DB) WithTxOptions(ctx context.Context, opts TxOptions, fn func(ctx context.Context) error) error {
	// Nested? use a savepoint
	if existing, ok := ctx.Value(ctxKeyTx).(pgx.Tx); ok {
		return withSavePoint(ctx, existing, fn)
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	max := 1
	if opts.RetryOnSerialization && opts.MaxRetries > 0 {
		max = opts.MaxRetries
	}

	var lastErr error
	for attempt := 1; attempt <= max; attempt++ {
		lastErr = d.runTxOnce(ctx, opts, fn)
		if lastErr == nil {
			return nil
		}

		if !opts.RetryOnSerialization || !IsSerializationFailure(lastErr) {
			return lastErr
		}

		d.log.Warn(
			"tx serializatiion failure, retrying",
			zap.Int("attempt", attempt),
			zap.Int("max", max),
			zap.Error(lastErr),
		)
		time.Sleep(backoff(attempt))
	}
	return fmt.Errorf("tx exhausted %d retries: %w", max, lastErr)
}

func (d *DB) runTxOnce(ctx context.Context, opts TxOptions, fn func(ctx context.Context) error) (retErr error) {
	txOpts := pgx.TxOptions{
		IsoLevel:       opts.Isolation,
		AccessMode:     opts.AccessMode,
		DeferrableMode: opts.DeferrableMode,
	}

	tx, err := d.pool.BeginTx(ctx, txOpts)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	txCtx := WithTxCtx(ctx, tx)

	// Panic-safe: always try to rollback if we haven't committed.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.Background())
			panic(p) // re-throw
		}

		if retErr != nil {
			if rbErr := tx.Rollback(context.Background()); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				d.log.Warn("rollback failed", zap.Error(rbErr))
			}
		}
	}()

	if err := fn(txCtx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// withSavepoint runs fn inside a nested savepoint on an existing tx.
func withSavePoint(ctx context.Context, tx pgx.Tx, fn func(ctx context.Context) error) (retErr error) {
	sp, err := tx.Begin(ctx) //pgx creates a savepoint on nested Begin
	if err != nil {
		return fmt.Errorf("savepoint begin: %w", err)
	}

	newCtx := WithTxCtx(ctx, tx)

	defer func() {
		if p := recover(); p != nil {
			_ = sp.Rollback(context.Background())
			panic(p)
		}

		if retErr != nil {
			if rbErr := sp.Rollback(context.Background()); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				retErr = fmt.Errorf("%w (savepoint rollback: %v)", retErr, rbErr)
			}
		}
	}()

	if err := fn(newCtx); err != nil {
		return err
	}

	return sp.Commit(ctx)
}

func backoff(attempt int) time.Duration {
	// 50ms, 100ms, 200ms, ...
	d := time.Duration(50*(1<<(attempt-1))) * time.Millisecond
	if d > 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	return d
}
