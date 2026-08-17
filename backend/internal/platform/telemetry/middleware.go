package telemetry

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func GinMiddleware(m *Metrics) gin.HandlerFunc {
	tracer := otel.Tracer(TracerName)
	propagator := otel.GetTextMapPropagator()

	return func(c *gin.Context) {
		// Continue an incoming trace (from a frontend, gateway, or another
		// service) instead of always starting a fresh one.
		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

		route := c.FullPath()
		if route == "" {
			// Unmatched (404) route. Fixed label — never the raw path,
			// which could contain arbitrary user input.
			route = "unmatched"
		}

		ctx, span := tracer.Start(ctx, c.Request.Method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.route", route),
				attribute.String("http.target", c.Request.URL.Path),
				attribute.String("http.client_ip", c.ClientIP()),
				attribute.String("http.user_agent", c.Request.UserAgent()),
			),
		)

		defer span.End()

		c.Request = c.Request.WithContext(ctx)

		m.IncInFlight()
		start := time.Now()

		c.Next() //Handler chain runs here, inside the span

		m.DecInFlight()
		dur := time.Since(start)
		status := c.Writer.Status()

		span.SetAttributes(attribute.Int("http.status_code", status))
		if status >= 500 {
			span.SetStatus(codes.Error, "server error")
		}
		if len(c.Errors) > 0 {
			span.RecordError(c.Errors.Last())
		}

		m.ObserveHTTP(c.Request.Method, route, status, dur, c.Writer.Size())
	}
}
