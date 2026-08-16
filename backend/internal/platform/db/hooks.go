package db

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/tracelog"
	"go.uber.org/zap"
)

// newQueryTracer builds a pgx QueryTracer that:
//   * logs slow queries (> statementTimeout/2, capped at 1s)
//   * exposes latency metrics (attach a MetricsCollector later)
//
// This is deliberately narrow — full log tracing lives behind DEBUG env level
// so it doesn't kill throughput in prod.

func newQueryTracer(log *zap.Logger, statementTimeout time.Duration) pgx.QueryTracer {
	slow := statementTimeout / 2
	if slow > time.Second {
		slow = time.Second
	}
	if slow == 0 {
		slow = 500 * time.Millisecond
	}

	return &tracelog.TraceLog{
		Logger: zapPgxAdapter{
			log:  log,
			slow: slow,
		},
		LogLevel: tracelog.LogLevelInfo,
	}
}

// zapPgxAdapter satisfies tracelog.Logger and emits only slow-query lines at
// info level; errors always log.
type zapPgxAdapter struct {
	log  *zap.Logger
	slow time.Duration
}

func (z zapPgxAdapter) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	// Detect and drop the built-in "Query" ok-lines that aren't slow.
	if level == tracelog.LogLevelInfo && msg == "Query" {
		if dur, ok := data["time"].(time.Duration); ok && dur < z.slow {
			return
		}
	}

	fields := make([]zap.Field, 0, len(data)+2)
	fields = append(fields, zap.String("event", msg))

	for k, v := range data {
		// Redact bind params — they can contain PII (email, phone, NID).
		if k == "args" {
			continue
		}
		if s, ok := v.(string); ok && k == "sql" {
			fields = append(fields, zap.String("sql", compactSQL(s)))
			continue
		}
		fields = append(fields, zap.Any(k, v))
	}
	// Propagate request-scoped identifiers if the middleware set them.
	if rid, _ := ctx.Value(ctxKeyRequestID).(string); rid != "" {
		fields = append(fields, zap.String("request_id", rid))
	}

	switch level {
	case tracelog.LogLevelError:
		z.log.Error("pg error", fields...)
	case tracelog.LogLevelWarn:
		z.log.Warn("pg warn", fields...)
	default:
		z.log.Info("pg slow", fields...)
	}
}

// compactSQL collapses whitespace so slow-query lines stay one row in Kibana.
func compactSQL(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")

	for strings.Contains(s, " ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return strings.TrimSpace(s)
}

// Tenant-scope helpers.
// The API's `tenant` middleware puts org_id/branch_id/user_id/request_id on
// the request context. Repositories read them from here to add mandatory
// WHERE clauses. This is defence-in-depth: even if a handler forgets, the
// repository will require the value and refuse to run.

// WithOrgID
func WithOrgID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyOrgID, id)
}

// WithBranchID
func WithBranchID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyBranchID, id)
}

// WithUserID
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

// WithRequestID: setters used by
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// OrgID
func OrgID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyOrgID).(string)
	return v
}

// BranchID
func BranchID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyBranchID).(string)
	return v
}

// UserID
func UserID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// RequestID: safe getters; empty string if missing
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// SoftDeleteFilter is a convenience constant used in SQL fragments. Import
// this in repositories instead of hard-coding the string.
const SoftDeleteFilter = "deleted_at IS NULL"
