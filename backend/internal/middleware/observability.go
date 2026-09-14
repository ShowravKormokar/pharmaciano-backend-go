package middleware

import "time"

// observeAuthzDecision is a nil-safe forward to the authz_decisions_total
// counter. It is the canonical call point from the Authorizer wrapper so the
// metric definition is owned by telemetry and we never dereference a nil
// *Metrics.
func (m *Middleware) observeAuthzDecision(outcome string) {
	if m == nil || m.metrics == nil {
		return
	}
	m.metrics.ObserveAuthzDecision(outcome)
}

// observeAuthzCache is a nil-safe forward to the authz_cache_total counter.
func (m *Middleware) observeAuthzCache(outcome string) {
	if m == nil || m.metrics == nil {
		return
	}
	m.metrics.ObserveAuthzCache(outcome)
}

// observeAuthzResolve is a nil-safe forward to the authz_resolve_seconds
// histogram.
func (m *Middleware) observeAuthzResolve(source string, dur time.Duration) {
	if m == nil || m.metrics == nil {
		return
	}
	m.metrics.ObserveAuthzResolve(source, dur)
}

// observeAccountEmailRateLimit is a nil-safe forward to the
// security_email_rate_limited_total counter so the email rate limiter never
// dereferences a nil *Metrics.
func (m *Middleware) observeAccountEmailRateLimit(bucket string) {
	if m == nil || m.metrics == nil {
		return
	}
	m.metrics.ObserveAccountEmailRateLimit(bucket)
}
