package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the small interface both *pgxpool.Pool and pgx.Tx satisfy. Repos
// depend on this so the same method works inside and outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Compile time assertions
var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = pgx.Tx(nil)
)

// contextKey is unexported to avoid collisions.
type contextKey int

const (
	ctxKeyTx contextKey = iota + 1
	ctxKeyOrgID
	ctxKeyBranchID
	ctxKeyUserID
	ctxKeyRequestID
)

// FromCtx returns a Querier: if a transaction is attached to ctx, it is
// returned; otherwise the pool. This lets repository methods be written
// without caring whether they're inside a tx.
func (d *DB) FromCtx(ctx context.Context) Querier {
	if tx, ok := ctx.Value(ctxKeyTx).(pgx.Tx); ok {
		return tx
	}
	return d.pool
}

// ReadFromCtx is like FromCtx but reads from the reader pool when there is
// no active transaction. Never routes a transactional read to the replica.
func (d *DB) ReadFromCtx(ctx context.Context) Querier {
	if tx, ok := ctx.Value(ctxKeyTx).(pgx.Tx); ok {
		return tx
	}
	return d.Reader()
}

// WithTxCtx stores tx on the context. Only transaction.WithTx should use this.
func WithTxCtx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, ctxKeyTx, tx)
}
