package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry *prometheus.Registry

	//HTTP
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPResponseSize    *prometheus.HistogramVec
	HTTPInFlight        prometheus.Gauge

	// DB — query-level. Pool-level gauges are wired separately via
	// RegisterDBPoolStats, because at NewMetrics() time no pool exists yet.
	DBQueryDuration *prometheus.HistogramVec
	DBQueriesTotal  *prometheus.CounterVec

	// Redis
	RedisOpDuration *prometheus.HistogramVec
	RedisOpsTotal   *prometheus.CounterVec

	// Auth — login, refresh, MFA, password, session-cache, account-lifecycle
	LoginAttemptsTotal      *prometheus.CounterVec // outcome: success | invalid_credentials | locked | inactive | unknown_email | mfa_required
	TokenIssuedTotal        *prometheus.CounterVec // kind: access | refresh
	TokenReuseDetected      prometheus.Counter
	RefreshAttemptsTotal    *prometheus.CounterVec // outcome: success | invalid_token | expired | reuse | session_inactive | account_inactive | error
	MFAChallengesTotal      *prometheus.CounterVec // outcome: success | invalid | required
	PasswordChangesTotal    *prometheus.CounterVec // outcome: success | wrong_current | weak_new | mismatch
	PasswordResetsIssued    *prometheus.CounterVec // outcome: known_user | unknown_user
	PasswordResetsRedeemed  *prometheus.CounterVec // outcome: success | invalid_token | expired
	AccountLockoutsTotal    *prometheus.CounterVec // outcome: triggered | released
	AccountEmailRateLimited *prometheus.CounterVec // bucket: login_forgot_reset

	// Session cache (ADR §17)
	SessionCacheHitsTotal   *prometheus.CounterVec // outcome: hit | miss | error | corrupt
	SessionCacheStoreTotal  *prometheus.CounterVec // outcome: stored | error
	SessionCacheInvalidated *prometheus.CounterVec // reason: logout | revoke_user | revoke_other | revoke_all | security_generation_bump

	// Authorization — allow/deny/cache/version-bump counters
	AuthzDecisionsTotal *prometheus.CounterVec // outcome: allow | deny | error
	AuthzCacheTotal     *prometheus.CounterVec // outcome: hit | miss | stale | error | skip
	AuthzResolveSeconds *prometheus.HistogramVec
	AuthzVersionBumps   *prometheus.CounterVec // scope: user | org
	RBACGenerationBumps *prometheus.CounterVec // reason: role_created | role_updated | role_deleted | role_permissions_set | explicit
	AuthzCacheErrors    prometheus.Counter     // count of corruption / read-error events observed in-process

	// Security / rate limiting / tenant / branch
	RateLimitedTotal       *prometheus.CounterVec // policy: ip | account_email | per_tenant | global
	SecurityDenialsTotal   *prometheus.CounterVec // reason: tenant_mismatch | branch_scope_denied | csrf | trusted_proxy
	TrustedProxyBlocked    *prometheus.CounterVec // reason: missing_xff | forwarded_for_untrusted
	StepUpRequiredTotal    *prometheus.CounterVec // outcome: satisfied | denied | skipped

	// Business
	SalesCompletedTotal    *prometheus.CounterVec
	PurchasesApprovedTotal *prometheus.CounterVec
	LowStockAlertsTotal    prometheus.Counter
	ExpiryAlertsTotal      prometheus.Counter

	// Jobs
	JobDuration   *prometheus.HistogramVec
	JobsProcessed *prometheus.CounterVec
	JobsFailed    *prometheus.CounterVec
	JobQueueSize  *prometheus.GaugeVec

	// AI
	AICallsTotal   *prometheus.CounterVec
	AICallDuration *prometheus.HistogramVec
	AITokensUsed   *prometheus.CounterVec
	AICostUSD      *prometheus.CounterVec

	// Mailer
	MailSendTotal    *prometheus.CounterVec
	MailSendDuration *prometheus.HistogramVec

	// Storage
	StorageOpsTotal   *prometheus.CounterVec
	StorageOpDuration *prometheus.HistogramVec
	StorageBytesTotal *prometheus.CounterVec
}

// New Matrics registers all collectors on fresh registry
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{Registry: reg}

	//HTTP collectors
	m.HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by method, route, status.",
		},
		[]string{"method", "route", "status"},
	)

	m.HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "route", "status"},
	)

	m.HTTPResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response size in bytes.",
			Buckets: prometheus.ExponentialBuckets(64, 4, 8),
		},
		[]string{"method", "route"},
	)

	m.HTTPInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_in_flight_requests", Help: "Currently in-flight requests.",
		},
	)

	// DB Collectors
	m.DBQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "SQL query latency.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		},
		[]string{"op"},
	)

	m.DBQueriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_queries_total",
			Help: "DB queries by op and outcome.",
		},
		[]string{"op", "outcome"},
	)

	// Redis Collector
	m.RedisOpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "redis_op_duration_seconds",
			Help:    "Redis operation latency.",
			Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		},
		[]string{"op"},
	)
	m.RedisOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_ops_total",
			Help: "Redis operations by op and outcome.",
		},
		[]string{"op", "outcome"},
	)

	// Auth Collector
	m.LoginAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_login_attempts_total",
			Help: "Login attempts by outcome.",
		},
		[]string{"outcome"}, // success | invalid_credentials | locked | inactive | unknown_email | mfa_required
	)
	m.TokenIssuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_tokens_issued_total",
			Help: "JWTs issued.",
		},
		[]string{"kind"}, // access | refresh
	)
	m.TokenReuseDetected = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "auth_refresh_reuse_detected_total",
			Help: "Detected refresh-token reuse events.",
		},
	)
	m.RefreshAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_refresh_attempts_total",
			Help: "Refresh attempts by outcome.",
		},
		[]string{"outcome"}, // success | invalid_token | expired | reuse | session_inactive | account_inactive | error
	)
	m.MFAChallengesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_mfa_challenges_total",
			Help: "MFA challenge outcomes.",
		},
		[]string{"outcome"}, // success | invalid | required
	)
	m.PasswordChangesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_password_changes_total",
			Help: "Password change outcomes.",
		},
		[]string{"outcome"}, // success | wrong_current | weak_new | mismatch
	)
	m.PasswordResetsIssued = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_password_resets_issued_total",
			Help: "Password reset issuance by outcome.",
		},
		[]string{"outcome"}, // known_user | unknown_user
	)
	m.PasswordResetsRedeemed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_password_resets_redeemed_total",
			Help: "Password reset redemption outcomes.",
		},
		[]string{"outcome"}, // success | invalid_token | expired
	)
	m.AccountLockoutsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_account_lockouts_total",
			Help: "Account lockout events by outcome.",
		},
		[]string{"outcome"}, // triggered | released
	)
	m.AccountEmailRateLimited = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_account_email_rate_limited_total",
			Help: "Distributed account/email rate-limiter hits by bucket.",
		},
		[]string{"bucket"}, // login | forgot | reset
	)

	// Session cache (ADR §17)
	m.SessionCacheHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_session_cache_total",
			Help: "Session cache lookups by outcome.",
		},
		[]string{"outcome"}, // hit | miss | error | corrupt
	)
	m.SessionCacheStoreTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_session_cache_store_total",
			Help: "Session cache store attempts by outcome.",
		},
		[]string{"outcome"}, // stored | error
	)
	m.SessionCacheInvalidated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_session_cache_invalidated_total",
			Help: "Session cache invalidation events by reason.",
		},
		[]string{"reason"}, // logout | revoke_user | revoke_other | revoke_all | security_generation_bump
	)

	// Authorization
	m.AuthzDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "authz_decisions_total",
			Help: "Authorization decisions by outcome.",
		},
		[]string{"outcome"}, // allow | deny | error
	)
	m.AuthzCacheTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "authz_cache_total",
			Help: "Authorization cache lookups by outcome.",
		},
		[]string{"outcome"}, // hit | miss | stale | error | skip
	)
	m.AuthzResolveSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "authz_resolve_seconds",
			Help:    "Authorization resolution latency.",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"source"}, // cache | enforcer | error
	)
	m.AuthzVersionBumps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "authz_version_bumps_total",
			Help: "Authorization version bumps by scope.",
		},
		[]string{"scope"}, // user | org
	)
	m.RBACGenerationBumps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rbac_generation_bumps_total",
			Help: "RBAC generation bumps by reason.",
		},
		[]string{"reason"}, // role_created | role_updated | role_deleted | role_permissions_set | explicit
	)
	m.AuthzCacheErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "authz_cache_errors_total",
			Help: "Authorization cache corruption / read-error events.",
		},
	)

	// Security
	m.RateLimitedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_rate_limited_total",
			Help: "Requests rejected by rate limiting by policy.",
		},
		[]string{"policy"}, // ip | account_email | per_tenant | global
	)
	m.SecurityDenialsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_denials_total",
			Help: "Requests denied by security middleware by reason.",
		},
		[]string{"reason"}, // tenant_mismatch | branch_scope_denied | csrf | trusted_proxy
	)
	m.TrustedProxyBlocked = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_trusted_proxy_blocked_total",
			Help: "Requests rejected by trusted-proxy policy.",
		},
		[]string{"reason"}, // missing_xff | forwarded_for_untrusted
	)
	m.StepUpRequiredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_step_up_total",
			Help: "Step-up authentication outcomes.",
		},
		[]string{"outcome"}, // satisfied | denied | skipped
	)

	// Sales Collector
	m.SalesCompletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sales_completed_total",
			Help: "Completed POS sales.",
		},
		[]string{"branch_id"}, // bounded (dozens of branches) — never sale_id/user_id here
	)
	m.PurchasesApprovedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "purchases_approved_total",
			Help: "Approved purchase orders.",
		},
		[]string{"branch_id"},
	)
	m.LowStockAlertsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "inventory_low_stock_alerts_total",
			Help: "Low-stock alerts fired.",
		},
	)
	m.ExpiryAlertsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "inventory_expiry_alerts_total",
			Help: "Expiry alerts fired.",
		},
	)

	// Jobs Collector
	m.JobDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "job_duration_seconds",
			Help:    "Asynq task duration.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
		},
		[]string{"queue", "task"},
	)
	m.JobsProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_processed_total",
			Help: "Completed background jobs.",
		},
		[]string{"queue", "task"},
	)
	m.JobsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_failed_total",
			Help: "Failed background jobs.",
		},
		// "reason" must be a short, caller-chosen bucket (e.g. "timeout",
		// "validation_error") — never the raw error string.
		[]string{"queue", "task", "reason"},
	)
	m.JobQueueSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "job_queue_size",
			Help: "Current queue depth.",
		},
		[]string{"queue"},
	)

	// AI Collector
	m.AICallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ai_calls_total",
			Help: "AI provider calls.",
		},
		[]string{"provider", "endpoint", "outcome"},
	)
	m.AICallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ai_call_duration_seconds",
			Help:    "AI provider latency.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"provider", "endpoint"},
	)
	m.AITokensUsed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ai_tokens_used_total",
			Help: "Tokens consumed.",
		},
		[]string{"provider", "endpoint", "direction"}, // in | out
	)
	m.AICostUSD = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ai_cost_usd_total",
			Help: "Cumulative USD spent.",
		},
		[]string{"provider", "endpoint"},
	)

	m.MailSendTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "mailer_send_total", Help: "Outbound mail/SMS attempts by provider and outcome."},
		[]string{"provider", "outcome"},
	)
	m.MailSendDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mailer_send_duration_seconds",
			Help:    "Outbound mail/SMS send latency.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"provider"},
	)

	m.StorageOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "storage_ops_total", Help: "File-store operations by driver, op, outcome."},
		[]string{"driver", "op", "outcome"},
	)
	m.StorageOpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "storage_op_duration_seconds",
			Help:    "File-store operation latency.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		},
		[]string{"driver", "op"},
	)
	m.StorageBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "storage_bytes_total", Help: "Bytes moved through the file store."},
		[]string{"driver", "op"},
	)

	reg.MustRegister(
		m.HTTPRequestsTotal, m.HTTPRequestDuration, m.HTTPResponseSize, m.HTTPInFlight,
		m.DBQueryDuration, m.DBQueriesTotal,
		m.RedisOpDuration, m.RedisOpsTotal,
		// Auth
		m.LoginAttemptsTotal, m.TokenIssuedTotal, m.TokenReuseDetected,
		m.RefreshAttemptsTotal, m.MFAChallengesTotal, m.PasswordChangesTotal,
		m.PasswordResetsIssued, m.PasswordResetsRedeemed,
		m.AccountLockoutsTotal, m.AccountEmailRateLimited,
		// Session cache
		m.SessionCacheHitsTotal, m.SessionCacheStoreTotal, m.SessionCacheInvalidated,
		// Authorization
		m.AuthzDecisionsTotal, m.AuthzCacheTotal, m.AuthzResolveSeconds,
		m.AuthzVersionBumps, m.RBACGenerationBumps, m.AuthzCacheErrors,
		// Security
		m.RateLimitedTotal, m.SecurityDenialsTotal, m.TrustedProxyBlocked, m.StepUpRequiredTotal,
		// Business / jobs / ai / mailer / storage
		m.SalesCompletedTotal, m.PurchasesApprovedTotal, m.LowStockAlertsTotal, m.ExpiryAlertsTotal,
		m.JobDuration, m.JobsProcessed, m.JobsFailed, m.JobQueueSize,
		m.AICallsTotal, m.AICallDuration, m.AITokensUsed, m.AICostUSD,
		m.MailSendTotal, m.MailSendDuration,
		m.StorageOpsTotal, m.StorageOpDuration, m.StorageBytesTotal,
	)

	return m
}

// ObserveHTTP is called once per request (by GinMiddleware). `route` must be
// the templated path (e.g. "/api/v1/sales/:id"), never the raw URL with real
// IDs in it, or every distinct ID creates a new time series (cardinality
// explosion).
func (m *Metrics) ObserveHTTP(method string, route string, status int, dur time.Duration, bytesOut int) {
	s := strconv.Itoa(status)

	m.HTTPRequestsTotal.WithLabelValues(method, route, s).Inc()
	m.HTTPRequestDuration.WithLabelValues(method, route, s).Observe(dur.Seconds())
	if bytesOut > 0 {
		m.HTTPResponseSize.WithLabelValues(method, route).Observe(float64(bytesOut))
	}
}

// IncInFlight / DecInFlight — exposed for non-Gin callers (e.g. worker HTTP
// endpoints). GinMiddleware calls these automatically for API routes.
func (m *Metrics) IncInFlight() {
	m.HTTPInFlight.Inc()
}
func (m *Metrics) DecInFlight() {
	m.HTTPInFlight.Dec()
}

type DBPoolStatsFunc func() (aquired, idle, total, maxConns int32)

// RegisterDBPoolStats wires connection-pool gauges into the registry. Call
// once per pool (primary, and again with a different poolName for the read
// replica if enabled) right after the pool is created in cmd/api / cmd/worker.
func (m *Metrics) RegisterDBPoolStats(poolName string, fn DBPoolStatsFunc) {
	labels := prometheus.Labels{"pool": poolName}
	m.Registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_acquired_connections", Help: "Connections currently checked out.", ConstLabels: labels,
		}, func() float64 { a, _, _, _ := fn(); return float64(a) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_idle_connections", Help: "Connections idle in the pool.", ConstLabels: labels,
		}, func() float64 { _, i, _, _ := fn(); return float64(i) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_total_connections", Help: "Total connections currently open.", ConstLabels: labels,
		}, func() float64 { _, _, t, _ := fn(); return float64(t) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_max_connections", Help: "Configured max pool size.", ConstLabels: labels,
		}, func() float64 { _, _, _, mx := fn(); return float64(mx) }),
	)
}

// Auth / business / job convenience methods — thin wrappers that keep label
// discipline in one place instead of every call site building label slices
// by hand (which is how cardinality bugs sneak in).
func (m *Metrics) ObserveLoginAttempt(outcome string) {
	m.LoginAttemptsTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveTokenIssued(kind string) {
	m.TokenIssuedTotal.WithLabelValues(kind).Inc()
}
func (m *Metrics) RecordTokenReuse() {
	m.TokenReuseDetected.Inc()
}
func (m *Metrics) ObserveRefreshAttempt(outcome string) {
	m.RefreshAttemptsTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveMFAChallenge(outcome string) {
	m.MFAChallengesTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObservePasswordChange(outcome string) {
	m.PasswordChangesTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObservePasswordResetIssued(outcome string) {
	m.PasswordResetsIssued.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObservePasswordResetRedeemed(outcome string) {
	m.PasswordResetsRedeemed.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveAccountLockout(outcome string) {
	m.AccountLockoutsTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveAccountEmailRateLimit(bucket string) {
	m.AccountEmailRateLimited.WithLabelValues(bucket).Inc()
}
func (m *Metrics) ObserveSessionCache(outcome string) {
	m.SessionCacheHitsTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveSessionCacheStore(outcome string) {
	m.SessionCacheStoreTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveSessionCacheInvalidate(reason string) {
	m.SessionCacheInvalidated.WithLabelValues(reason).Inc()
}
func (m *Metrics) ObserveAuthzDecision(outcome string) {
	m.AuthzDecisionsTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveAuthzCache(outcome string) {
	m.AuthzCacheTotal.WithLabelValues(outcome).Inc()
}
func (m *Metrics) ObserveAuthzResolve(source string, dur time.Duration) {
	m.AuthzResolveSeconds.WithLabelValues(source).Observe(dur.Seconds())
}
func (m *Metrics) ObserveAuthzVersionBump(scope string) {
	m.AuthzVersionBumps.WithLabelValues(scope).Inc()
}
func (m *Metrics) ObserveRBACGenerationBump(reason string) {
	m.RBACGenerationBumps.WithLabelValues(reason).Inc()
}
func (m *Metrics) IncAuthzCacheError() {
	m.AuthzCacheErrors.Inc()
}
func (m *Metrics) ObserveRateLimited(policy string) {
	m.RateLimitedTotal.WithLabelValues(policy).Inc()
}
func (m *Metrics) ObserveSecurityDenial(reason string) {
	m.SecurityDenialsTotal.WithLabelValues(reason).Inc()
}
func (m *Metrics) ObserveTrustedProxyBlocked(reason string) {
	m.TrustedProxyBlocked.WithLabelValues(reason).Inc()
}
func (m *Metrics) ObserveStepUp(outcome string) {
	m.StepUpRequiredTotal.WithLabelValues(outcome).Inc()
}

func (m *Metrics) ObserveSaleCompleted(branchID string) {
	m.SalesCompletedTotal.WithLabelValues(branchID).Inc()
}
func (m *Metrics) ObservePurchaseApproved(branchID string) {
	m.PurchasesApprovedTotal.WithLabelValues(branchID).Inc()
}
func (m *Metrics) RecordLowStockAlert() {
	m.LowStockAlertsTotal.Inc()
}
func (m *Metrics) RecordExpiryAlert() {
	m.ExpiryAlertsTotal.Inc()
}

// ObserveJob records one completed job attempt. Pass failReason == "" for a
// success; otherwise pass a short, bounded bucket name (e.g. "timeout"),
// never err.Error() (unbounded cardinality).
func (m *Metrics) Observejob(queue, task string, dur time.Duration, failReason string) {
	m.JobDuration.WithLabelValues(queue, task).Observe(dur.Seconds())
	if failReason != "" {
		m.JobsFailed.WithLabelValues(queue, task, failReason).Inc()
		return
	}
	m.JobsProcessed.WithLabelValues(queue, task).Inc()
}

func (m *Metrics) SetJobQueueSize(queue string, size int) {
	m.JobQueueSize.WithLabelValues(queue).Set(float64(size))
}

// Handler returns the /metrics HTTP handler. When authToken is non-empty,
// requests must carry "Authorization: Bearer <authToken>" — use this in any
// environment where the metrics port might be reachable outside a trusted
// network (e.g. shared VPS without a firewall in front of :9091).
func (m *Metrics) Handler(authToken string) http.Handler {
	h := promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{
		Registry:          m.Registry,
		EnableOpenMetrics: true,
	})

	if authToken == "" {
		return h
	}

	want := "Bearer" + authToken
	return http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
		if rq.Header.Get("Authorization") != want {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(rw, rq)
	})
}

func (m *Metrics) ObserveStorageOp(driver, op string, dur time.Duration, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.StorageOpsTotal.WithLabelValues(driver, op, outcome).Inc()
	m.StorageOpDuration.WithLabelValues(driver, op).Observe(dur.Seconds())
}

func (m *Metrics) AddStorageBytes(driver, op string, n int64) {
	if n <= 0 {
		return
	}
	m.StorageBytesTotal.WithLabelValues(driver, op).Add(float64(n))
}
