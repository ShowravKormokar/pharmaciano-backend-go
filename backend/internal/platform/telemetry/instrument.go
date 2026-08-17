// -----------------------------------------------------------------------------
// ===== File: internal/platform/telemetry/instrument.go ========================
// -----------------------------------------------------------------------------
package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// TracedOperation wraps fn with a span (attrs) and calls onDone with the
// elapsed duration and error so the caller can record a matching Prometheus
// metric. This is the single place where "one call site produces both a
// span and a metric" — DB/Redis/AI helpers below are just callers of this.
func TracedOperation(
	ctx context.Context,
	spanName string,
	attrs []attribute.KeyValue,
	fn func(ctx context.Context) error,
	onDone func(dur time.Duration, err error),
) error {
	ctx, span := StartSpan(ctx, spanName, attrs...)
	start := time.Now()
	err := fn(ctx)
	dur := time.Since(start)
	EndSpan(span, err)
	if onDone != nil {
		onDone(dur, err)
	}
	return err
}

// TraceDBQuery wraps a repository call with a DB span + db_query_duration_seconds
// / db_queries_total. `op` must be a short logical name — "user.FindByEmail",
// "sale.InsertItems" — never raw SQL or an entity ID.
//
//	err := metrics.TraceDBQuery(ctx, "user.FindByEmail", func(ctx context.Context) error {
//	    return pool.QueryRow(ctx, sql, email).Scan(&u.ID, &u.Email)
//	})
func (m *Metrics) TraceDBQuery(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	attrs := []attribute.KeyValue{
		AttrDBSystem.String("postgresql"),
		AttrDBOperation.String(op),
	}
	return TracedOperation(ctx, "db."+op, attrs, fn, func(dur time.Duration, err error) {
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		m.DBQueriesTotal.WithLabelValues(op, outcome).Inc()
		m.DBQueryDuration.WithLabelValues(op).Observe(dur.Seconds())
	})
}

// TraceRedisOp mirrors TraceDBQuery for Redis calls.
//
//	err := metrics.TraceRedisOp(ctx, "session.get", func(ctx context.Context) error {
//	    _, err := redisClient.Get(ctx, key)
//	    return err
//	})
func (m *Metrics) TraceRedisOp(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	attrs := []attribute.KeyValue{AttrRedisOperation.String(op)}
	return TracedOperation(ctx, "redis."+op, attrs, fn, func(dur time.Duration, err error) {
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		m.RedisOpsTotal.WithLabelValues(op, outcome).Inc()
		m.RedisOpDuration.WithLabelValues(op).Observe(dur.Seconds())
	})
}

// TraceAICall wraps an outbound AI provider call. `endpoint` must be a
// logical operation name ("chat_completion", "forecast") — never a dynamic
// URL with a request ID in it.
//
//	err := metrics.TraceAICall(ctx, "openai", "chat_completion", func(ctx context.Context) error {
//	    resp, err = aiClient.Do(ctx, req)
//	    return err
//	})
func (m *Metrics) TraceAICall(ctx context.Context, provider, endpoint string, fn func(ctx context.Context) error) error {
	attrs := []attribute.KeyValue{
		AttrAIProvider.String(provider),
		AttrAIEndpoint.String(endpoint),
	}
	return TracedOperation(ctx, "ai."+provider+"."+endpoint, attrs, fn, func(dur time.Duration, err error) {
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		m.AICallsTotal.WithLabelValues(provider, endpoint, outcome).Inc()
		m.AICallDuration.WithLabelValues(provider, endpoint).Observe(dur.Seconds())
	})
}

// RecordAITokens / RecordAICost are separate from TraceAICall because token
// counts and cost are usually only known after a successful response body
// is parsed, i.e. after fn() already returned.
func (m *Metrics) RecordAITokens(provider, endpoint, direction string, count int) {
	m.AITokensUsed.WithLabelValues(provider, endpoint, direction).Add(float64(count))
}
func (m *Metrics) RecordAICost(provider, endpoint string, usd float64) {
	m.AICostUSD.WithLabelValues(provider, endpoint).Add(usd)
}