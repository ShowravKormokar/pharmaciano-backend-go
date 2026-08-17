package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"go.uber.org/zap"
)

// LoggerFromContext returns base enriched with trace_id/span_id when ctx
// carries a valid span. Use this at the top of service/handler methods
// instead of the bare logger, so every error log line can be pivoted to in
// Jaeger/Tempo:
//
//	log := telemetry.LoggerFromContext(ctx, baseLogger)
//	log.Error("sale creation failed", zap.Error(err))
func LoggerFromContext(ctx context.Context, base *zap.Logger) *zap.Logger {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return base
	}

	return base.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
}
