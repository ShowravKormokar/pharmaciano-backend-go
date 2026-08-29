package middleware

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/config"
	"backend/internal/platform/redis"
	"backend/internal/platform/telemetry"
	"backend/pkg/response"
)

// Injected contracts — implemented by the domain modules, consumed here.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (appctx.Principal, error)
}

type Authorizer interface {
	Enforce(ctx context.Context, sub, dom, obj, act string) (bool, error)
}

// AuditSink receives exactly one entry per audited (state-changing) request.
type AuditSink interface {
	Record(ctx context.Context, entry AuditEntry)
}

// Middleware — the dependency container. Every middleware is a method on it.

// Middleware carries the dependencies shared by all middleware.
type Middleware struct {
	cfg   *config.Config
	log   *zap.Logger
	redis *redis.Client

	authn Authenticator
	authz Authorizer
	audit AuditSink

	// now is an injectable clock so time-sensitive middleware (rate limiting, token-bucket refills) are deterministic in tests. Defaults to time.Now.
	now func() time.Time
}

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

// Convenience chains — the router composes these, but they document the canonical ordering in one place.

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

func (m *Middleware) Protected(module, action string) []gin.HandlerFunc {
	return []gin.HandlerFunc{
		m.Auth(),
		m.Tenant(),
		m.RBAC(module, action),
	}
}

// gin context keys — small, package-private, string-typed (Gin's store is map[string]any). Used to pass hints between middleware (e.g. RBAC → Audit).

const (
	ginKeyAuditModule = "mc_audit_module"
	ginKeyAuditAction = "mc_audit_action"
	ginKeyErrorCode   = "mc_error_code"
)

// Shared helpers.

func routeOf(c *gin.Context) string {
	if r := c.FullPath(); r != "" {
		return r
	}
	return "unmatched"
}

// logFor returns a request-scoped logger enriched with trace/span ids (from the telemetry span) plus request-id, method, route and client ip.
func (m *Middleware) logFor(c *gin.Context) *zap.Logger {
	ctx := c.Request.Context()
	return telemetry.LoggerFromContext(ctx, m.log).With(
		zap.String("request_id", appctx.RequestID(ctx)),
		zap.String("method", c.Request.Method),
		zap.String("route", routeOf(c)),
		zap.String("client_ip", c.ClientIP()),
	)
}

func bearerToken(h string) (token string, ok bool) {
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token = strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}


func (m *Middleware) abortError(c *gin.Context, err error) {
	if c.IsAborted() {
		return
	}
	rid := appctx.RequestID(c.Request.Context())
	status := errs.HTTPStatus(err)
	code := errs.CodeOf(err)

	// Stash the code so a downstream Audit entry can record precisely why the request failed, without re-deriving it from the status.
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

// toResponseDetails converts the errors package's FieldError slice into the response package's shape (the two are intentionally decoupled).
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

// Fail-closed / no-op default implementations. Replaced via New's options once the auth, rbac and audit modules are built.

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
