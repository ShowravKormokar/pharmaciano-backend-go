package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	"backend/internal/platform/config"
)

// TracerName identifies this codebase's spans in trace backends (Jaeger/
// Tempo). Every span created via StartSpan or GinMiddleware carries this
// instrumentation name — keep it stable, or traces fragment across two
// "libraries" in the UI.
const TracerName = "pharmaciano-erp"

// Tracer owns the process-wide TracerProvider and its shutdown/flush hook.
type Tracer struct {
	tp       *sdktrace.TracerProvider
	shutdown func(ctx context.Context) error
	log      *zap.Logger
}

// InitTracing configures the global OTel tracer provider from config. It
// ALWAYS returns a non-nil *Tracer, and Shutdown is always safe to call —
// so main() can unconditionally `defer tracer.Shutdown(ctx)` regardless of
// whether tracing.enabled is true.
func InitTracing(ctx context.Context, cfg config.TelemetryConfig, appName, appVersion, env string, log *zap.Logger) (*Tracer, error) {
	if !cfg.Tracing.Enabled {
		log.Info("tracing disabled")
		otel.SetTracerProvider(noop.NewTracerProvider())
		return &Tracer{log: log}, nil
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(cfg.Tracing.Endpoint)}
	if cfg.Tracing.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exp, err := otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(orDefault(cfg.Tracing.ServiceName, appName)),
			semconv.ServiceVersion(appVersion),
			semconv.DeploymentEnvironmentName(env),
			// semconv.DeploymentEnvironment(env),
		),
	)

	if err != nil {
		return nil, err
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(clamp(cfg.Tracing.SamplingRatio, 0, 1)))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(
			exp,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Info(
		"tracing enabled",
		zap.String("exporter", cfg.Tracing.Exporter),
		zap.String("endpoint", cfg.Tracing.Endpoint),
		zap.Float64("sample_ration", cfg.Tracing.SamplingRatio),
	)

	return &Tracer{
		tp: tp,
		shutdown: func(ctx context.Context) error {
			return errors.Join(tp.Shutdown(ctx), exp.Shutdown(ctx))
		},
		log: log,
	}, nil
}

// Shutdown flushes buffered spans. Safe to call even when tracing was
// disabled (shutdown is nil in that case) or on a nil *Tracer.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.shutdown == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return t.shutdown(shutdownCtx)
}

func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tr := otel.Tracer(TracerName)
	return tr.Start(ctx, name, trace.WithAttributes(attrs...))
}

// EndSpan records err (if non-nil) as an error status + event, then ends the
// span. Centralizing this keeps every call site's error handling identical.
func EndSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func clamp(v, lo, hi float64) float64 {
	switch {
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}
