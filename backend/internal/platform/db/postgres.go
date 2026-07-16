package db

import (
	"backend/internal/platform/config"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// DB is the primary read-write pool wrapper. It is the type that repositories
// depend on. Reads that must go to a replica use Reader() instead.
type DB struct {
	pool   *pgxpool.Pool
	reader *pgxpool.Pool
	log    *zap.Logger
	cfg    config.DatabaseConfig
}

// New builds the primary pool
func New(ctx context.Context, cfg config.DatabaseConfig, log *zap.Logger) (*DB, error) {
	primary, err := buildPool(ctx, dsnFromConfig(cfg), cfg, log.Named("pg.primary"), false)
	if err != nil {
		return nil, fmt.Errorf("db.New primary: %w", err)
	}

	d := &DB{pool: primary, log: log, cfg: cfg}

	if cfg.ReadReplica.Enabled {
		reader, err := buildPool(ctx, dsnFromReplica(cfg), cfg, log.Named("pg.reader"), true)
		if err != nil {
			primary.Close()
			return nil, fmt.Errorf("db.New reader: %w", err)
		}
		d.reader = reader
	}

	// Ping both to fail fast at boot.
	if err := d.Ping(ctx); err != nil {
		d.Close()
		return nil, err
	}
	log.Info("postgres pools ready",
		zap.String("host", cfg.Host),
		zap.String("db", cfg.Name),
		zap.Int32("max_conns", cfg.Pool.MaxConns),
		zap.Bool("replica", cfg.ReadReplica.Enabled),
	)
	return d, nil
}

// Pool returns the primary read-write pool. Use this in repository
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Reader returns the replica pool if configured, otherwise the primary.
// Applications should use Reader() only for endpoints that tolerate replica lag.
func (d *DB) Reader() *pgxpool.Pool {
	if d.reader != nil {
		return d.reader
	}
	return d.pool
}

// Ping runs a lightweight round-trip against both pools
func (d *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("primary ping: %w", err)
	}
	if d.reader != nil {
		if err := d.reader.Ping(ctx); err != nil {
			return fmt.Errorf("reader ping: %w", err)
		}
	}
	return nil
}

// Stats returns snapshot metrics for observability.
type PoolStats struct {
	Acquired        int32
	Idle            int32
	Total           int32
	MaxConns        int32
	NewConnsCount   int64
	AcquireCount    int64
	AcquireDuration time.Duration
}

func (d *DB) Stats() PoolStats {
	s := d.pool.Stat()
	return PoolStats{
		Acquired:        s.AcquiredConns(),
		Idle:            s.IdleConns(),
		Total:           s.TotalConns(),
		MaxConns:        s.MaxConns(),
		NewConnsCount:   s.NewConnsCount(),
		AcquireCount:    s.AcquireCount(),
		AcquireDuration: s.AcquireDuration(),
	}
}

// Close releases both pools. Idempotent
func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
	if d.reader != nil {
		d.reader.Close()
	}
}

// Internals
func buildPool(ctx context.Context, dsn string, cfg config.DatabaseConfig, log *zap.Logger, readOnly bool) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	// Pool sizing
	pc.MaxConns = cfg.Pool.MaxConns
	pc.MinConns = cfg.Pool.MinConns
	pc.MaxConnLifetime = cfg.Pool.MaxConnLifetime
	pc.MaxConnIdleTime = cfg.Pool.MaxConnIdleTime
	pc.HealthCheckPeriod = cfg.Pool.HealthCheckPeriod

	// Per-session setting applied  on every new connection
	pc.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		stmts := []string{
			// Force UTC on the wire; app layer converts to org timezone.
			`SET TIME ZONE 'UTC'`,
			// Statement timeout — belt & braces around context deadlines.
			fmt.Sprintf(`SET statement_timeout = %d`, cfg.StatementTimeout.Milliseconds()),
			// Lock waits > 5s should fail rather than pile up.
			`SET lock_timeout = 5000`,
			// Idle-in-transaction protection.
			`SET idle_in_transaction_session_timeout = 30000`,
		}

		if readOnly {
			stmts = append(stmts, `SET default_transaction_read_only = on`)
		}

		for _, s := range stmts {
			if _, err := conn.Exec(ctx, s); err != nil {
				return fmt.Errorf("afterConnect %q: %w", s, err)
			}
		}

		return nil
	}

	// Attach the query tracer (slow-query + optional debug log). See hooks.go.
	pc.ConnConfig.Tracer = newQueryTracer(log, cfg.StatementTimeout)

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func dsnFromConfig(cfg config.DatabaseConfig) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s&application_name=medicore-api&pool_max_conns=%d",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Name, cfg.SSLMode, cfg.Pool.MaxConns,
	)
}

func dsnFromReplica(cfg config.DatabaseConfig) string {
	r := cfg.ReadReplica
	user := r.User
	if user == "" {
		user = cfg.User
	}
	pass := r.Password
	if pass == "" {
		pass = cfg.Password
	}
	port := r.Port
	if port == 0 {
		port = 5432
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s&application_name=medicore-api-ro&pool_max_conns=%d",
		user, pass, r.Host, port, cfg.Name, cfg.SSLMode, cfg.Pool.MaxConns,
	)
}

// ErrNoRows is re-exported so repositories don't need to import pgx directly.
var ErrNoRows = pgx.ErrNoRows

// IsUniqueViolation reports whether err is a "23505" (unique_violation).
func IsUniqueVilation(err error) bool {
	return matchSQLState(err, "23505")
}

// IsForeignKeyViolation reports whether err is a "23503".
func IsForeignKeyViolation(err error) bool {
	return matchSQLState(err, "23503")
}

// IsCheckViolation reports whether err is a "23514".
func IsCheckViolation(err error) bool {
	return matchSQLState(err, "23514")
}

// IsSerializationFailure reports whether err is a "40001" (retriable).
func IsSerializationFailure(err error) bool {
	return matchSQLState(err, "40001")
}

func matchSQLState(err error, code string) bool {
	if err == nil {
		return false
	}

	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == code
	}
	return false
}
