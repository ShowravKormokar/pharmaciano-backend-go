// Package middleware holds every HTTP middleware in the system. It sits
// between the platform layer (config, redis, logger, telemetry — all "done")
// and the domain modules (auth, rbac, user, … — still being built), and is
// deliberately written so it can compile and be unit-tested *before* those
// modules exist.
//
// # Why interfaces live here (dependency inversion)
//
// The naive design has auth.go import the `auth` module and rbac.go import the
// `rbac` module. But the modules must, in turn, import this package to protect
// their own routes — that is an import cycle, and it also means middleware
// can't be built until the modules are. We break the cycle by *inverting the
// dependency*: this package declares the small behavioural contracts it needs
// (Authenticator, Authorizer, AuditSink) and the concrete implementations are
// injected at wiring time (cmd/api/main.go). Middleware depends only on the
// platform/common/errors/pkg packages that already exist.
//
// # Chain order (outermost → innermost)
//
//	request_id → telemetry(span) → recovery → access-log → security-headers →
//	cors → body-limit → [timeout] → [rate-limit] → auth → tenant → rbac →
//	[idempotency] → handler → [audit]
//
// The first six form the always-on Global() chain. auth→tenant→rbac form the
// Protected() chain: "token valid → scope set → permission checked → handler",
// exactly as the architecture plan specifies. Bracketed stages are applied
// per route-family by the router.
//
// # Fail-closed by default
//
// Until a real Authenticator/Authorizer is injected, New() installs deny-all
// stubs: protected routes answer 401/403 rather than silently allowing access.
// Only the rate limiter fails *open* (documented in rate_limit.go), because a
// Redis outage must not lock every user out of the whole system.
package middleware

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	"backend/internal/common/constants"
	errs "backend/internal/errors"
	"backend/internal/platform/config"
	"backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
	"backend/pkg/response"
)

// -----------------------------------------------------------------------------
// Injected contracts — implemented by the domain modules, consumed here.
// -----------------------------------------------------------------------------

// Authenticator turns a raw bearer token into an authenticated Principal.
//
// The concrete implementation (the `auth` module) owns everything security
// sensitive: pinned-algorithm JWT verification, session lookup, JTI blacklist
// checks, cached user-status re-validation, and the choice between a stateless
// or hybrid-stateful strategy. Because that choice lives *behind* this
// interface, switching strategies later is a one-line change at wiring time
// and needs no edit to auth.go. On failure it must return an *errs.AppError so
// the middleware can map it to the correct 401/403 and code.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (appctx.Principal, error)
}

// Authorizer answers "may `sub` perform `act` on `obj` inside domain `dom`?" —
// the classic Casbin RBAC-with-domains signature. `dom` is the tenant (org)
// id, so a policy can never leak across organizations. Implemented by the
// `rbac` module (Casbin enforcer). Returning an error is treated as "deny"
// and logged; enforcement never fails open.
type Authorizer interface {
	Enforce(ctx context.Context, sub, dom, obj, act string) (bool, error)
}

// AuditSink receives exactly one entry per audited (state-changing) request.
//
// Implementations MUST be non-blocking — enqueue onto Asynq / a buffered
// channel and return immediately. Record runs on the request hot path after
// the handler completes; blocking here would add its latency to every write.
type AuditSink interface {
	Record(ctx context.Context, entry AuditEntry)
}

// -----------------------------------------------------------------------------
// Middleware — the dependency container. Every middleware is a method on it.
// -----------------------------------------------------------------------------

// Middleware carries the dependencies shared by all middleware. Construct it
// once at start-up with New and register its methods on the router.
type Middleware struct {
	cfg   *config.Config
	log   *zap.Logger
	redis *redis.Client

	authn Authenticator
	authz Authorizer
	audit AuditSink

	// now is an injectable clock so time-sensitive middleware (rate limiting,
	// token-bucket refills) are deterministic in tests. Defaults to time.Now.
	now func() time.Time
}

// Option customises a Middleware at construction. Real implementations are
// injected here at wiring time; anything left unset keeps its fail-closed
// (or no-op) default.
type Option func(*Middleware)

// WithAuthenticator injects the real token/session authenticator.
func WithAuthenticator(a Authenticator) Option {
	return func(m *Middleware) {
		if a != nil {
			m.authn = a
		}
	}
}

// WithAuthorizer injects the real RBAC enforcer.
func WithAuthorizer(a Authorizer) Option {
	return func(m *Middleware) {
		if a != nil {
			m.authz = a
		}
	}
}

// WithAuditSink injects the real audit sink (Asynq producer).
func WithAuditSink(s AuditSink) Option {
	return func(m *Middleware) {
		if s != nil {
			m.audit = s
		}
	}
}

// WithClock overrides the wall clock (tests only).
func WithClock(now func() time.Time) Option {
	return func(m *Middleware) {
		if now != nil {
			m.now = now
		}
	}
}

// New builds the middleware container. cfg, log and rdb are required platform
// dependencies. Domain dependencies default to safe fail-closed stubs and are
// overridden via options once their modules exist.
func New(cfg *config.Config, log *zap.Logger, rdb *redis.Client, opts ...Option) *Middleware {
	if log == nil {
		log = zap.NewNop()
	}
	m := &Middleware{
		cfg:   cfg,
		log:   log,
		redis: rdb,
		authn: denyAllAuthenticator{},
		authz: denyAllAuthorizer{},
		audit: nopAuditSink{},
		now:   time.Now,
	}
	for _, o := range opts {
		o(m)
	}

	// Loudly flag stub wiring so nobody ships a build where auth is a no-op
	// without knowing it.
	if _, stub := m.authn.(denyAllAuthenticator); stub {
		log.Warn("middleware: no Authenticator injected — every protected route will answer 401 (stub wiring)")
	}
	if _, stub := m.authz.(denyAllAuthorizer); stub {
		log.Warn("middleware: no Authorizer injected — every RBAC check will deny (stub wiring)")
	}
	if _, stub := m.audit.(nopAuditSink); stub {
		log.Warn("middleware: no AuditSink injected — audit events will be dropped (stub wiring)")
	}
	return m
}

// -----------------------------------------------------------------------------
// Convenience chains — the router composes these, but they document the
// canonical ordering in one place.
// -----------------------------------------------------------------------------

// Global returns the always-on middleware applied to every request, in order.
// The router prepends telemetry.GinMiddleware (span + metrics) so this list
// starts at request-id and runs inside the server span.
func (m *Middleware) Global() []gin.HandlerFunc {
	return []gin.HandlerFunc{
		m.RequestID(),
		m.Recovery(),
		m.AccessLog(),
		m.SecurityHeaders(),
		m.CORS(),
		m.BodyLimit(),
	}
}

// Protected returns the auth→tenant→rbac chain for a route guarded by the
// given permission (module:action). Rate-limit, idempotency, timeout and audit
// are layered on per route-family by the router where they apply.
func (m *Middleware) Protected(module, action string) []gin.HandlerFunc {
	return []gin.HandlerFunc{
		m.Auth(),
		m.Tenant(),
		m.RBAC(module, action),
	}
}

// -----------------------------------------------------------------------------
// gin context keys — small, package-private, string-typed (Gin's store is
// map[string]any). Used to pass hints between middleware (e.g. RBAC → Audit).
// -----------------------------------------------------------------------------

const (
	ginKeyAuditModule = "mc_audit_module"
	ginKeyAuditAction = "mc_audit_action"
	ginKeyErrorCode   = "mc_error_code"
)

// -----------------------------------------------------------------------------
// Shared helpers.
// -----------------------------------------------------------------------------

// routeOf returns the matched route template (never the raw path, which can
// contain user input / high-cardinality ids). Falls back to a fixed label.
func routeOf(c *gin.Context) string {
	if r := c.FullPath(); r != "" {
		return r
	}
	return "unmatched"
}

// logFor returns a request-scoped logger enriched with trace/span ids (from
// the telemetry span) plus request-id, method, route and client ip.
func (m *Middleware) logFor(c *gin.Context) *zap.Logger {
	ctx := c.Request.Context()
	return telemetry.LoggerFromContext(ctx, m.log).With(
		zap.String("request_id", appctx.RequestID(ctx)),
		zap.String("method", c.Request.Method),
		zap.String("route", routeOf(c)),
		zap.String("client_ip", c.ClientIP()),
	)
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. The scheme match is case-insensitive per RFC 7235; the token itself
// is returned untouched. ok is false when the header is missing or malformed.
func bearerToken(h string) (token string, ok bool) {
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token = strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// abortError is the single place every middleware renders an error. It logs at
// the taxonomy-assigned severity (with trace correlation), sets Retry-After
// when the error carries one, never leaks internals on 5xx, and writes the
// canonical envelope before aborting the chain.
func (m *Middleware) abortError(c *gin.Context, err error) {
	if c.IsAborted() {
		return
	}
	rid := appctx.RequestID(c.Request.Context())
	status := errs.HTTPStatus(err)
	code := errs.CodeOf(err)

	// Stash the code so a downstream Audit entry can record precisely why the
	// request failed, without re-deriving it from the status.
	c.Set(ginKeyErrorCode, string(code))

	lg := m.logFor(c)
	fields := []zap.Field{
		zap.Int("status", status),
		zap.String("error_code", string(code)),
		zap.Error(err),
	}
	switch errs.LevelFor(err) {
	case errs.LogLevelError:
		lg.Error("request failed", fields...)
	case errs.LogLevelWarn:
		lg.Warn("request rejected", fields...)
	default:
		lg.Info("request rejected", fields...)
	}

	// 5xx: fixed, generic body — the cause is in the log, never on the wire.
	if status >= 500 {
		_ = response.Internal(c.Writer, rid)
		c.Abort()
		return
	}

	ae := errs.As(err)
	if ae == nil {
		_ = response.Error(c.Writer, rid, status, string(errs.CodeInternal), "internal server error")
		c.Abort()
		return
	}

	if ra := errs.RetryAfter(err); ra > 0 {
		c.Writer.Header().Set(constants.HeaderRetryAfter, strconv.Itoa(ra))
	}
	_ = response.Error(c.Writer, rid, status, string(ae.Code), ae.Message, toResponseDetails(ae.Details)...)
	c.Abort()
}

// toResponseDetails converts the errors package's FieldError slice into the
// response package's shape (the two are intentionally decoupled).
func toResponseDetails(in []errs.FieldError) []response.FieldError {
	if len(in) == 0 {
		return nil
	}
	out := make([]response.FieldError, len(in))
	for i, f := range in {
		out[i] = response.FieldError{
			Field:   f.Field,
			Rule:    f.Rule,
			Message: f.Message,
			Value:   f.Value,
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Fail-closed / no-op default implementations. Replaced via New's options once
// the auth, rbac and audit modules are built.
// -----------------------------------------------------------------------------

type denyAllAuthenticator struct{}

func (denyAllAuthenticator) Authenticate(context.Context, string) (appctx.Principal, error) {
	return appctx.Principal{}, errs.Unauthenticated().
		WithMeta("reason", "authenticator not configured")
}

type denyAllAuthorizer struct{}

func (denyAllAuthorizer) Enforce(context.Context, string, string, string, string) (bool, error) {
	return false, nil
}

type nopAuditSink struct{}

func (nopAuditSink) Record(context.Context, AuditEntry) {}
