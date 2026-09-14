package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	uuidx "backend/internal/platform/uuid"
)

// AuthRealm is the protection space advertised in the WWW-Authenticate challenge. Clients group credentials by realm; a single stable label is fine.
const authRealm = "pharmaciano"

func (m *Middleware) Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			// No usable credentials: bare challenge, no error code (RFC 6750 §3).
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="`+authRealm+`"`)
			m.abortError(c, errs.Unauthenticated().
				WithMeta("reason", "missing or malformed Authorization header"))
			return
		}

		principal, err := m.authn.Authenticate(c.Request.Context(), token)
		if err != nil {
			// Credentials were supplied but rejected: challenge with the machine -readable reason so compliant clients know whether to refresh.
			c.Writer.Header().Set("WWW-Authenticate", wwwAuthenticate(err))
			m.abortError(c, err)
			return
		}

		if uuidx.IsNil(principal.UserID) || uuidx.IsNil(principal.OrgID) {
			c.Writer.Header().Set("WWW-Authenticate", wwwAuthenticate(errs.Unauthenticated()))
			m.abortError(c, errs.Unauthenticated().
				WithMeta("reason", "authenticator returned an incomplete principal"))
			return
		}

		// Promote the identity into the request context for the rest of the chain.
		ctx := appctx.WithPrincipal(c.Request.Context(), principal)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// IdleSession enforces the configured idle timeout (config.Session.IdleTimeout).
// It runs after Auth so the session id is on the context. On every request it
// checks whether the session has been idle longer than the configured window;
// if so, it rejects the request with 401 Unauthenticated (client must log in
// again). When the session is still active, it touches last_seen_at atomically
// via the injected SessionToucher so the idle clock resets.
//
// A zero or negative IdleTimeout disables the middleware entirely (it is a
// no-op pass-through). A nil SessionToucher also degrades to pass-through so
// the chain is testable without a database.
func (m *Middleware) IdleSession() gin.HandlerFunc {
	if m == nil || m.cfg == nil || m.cfg.Session.IdleTimeout <= 0 || m.toucher == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		sessionID := appctx.SessionID(ctx)
		if sessionID == uuid.Nil {
			m.abortError(c, errs.Unauthenticated().
				WithMeta("reason", "idle-session check requires an authenticated session"))
			return
		}
		now := m.now()
		ip := appctx.ClientIP(ctx)
		var ipPtr *string
		if ip != "" {
			ipPtr = &ip
		}
		if active, err := m.toucher.TouchSession(ctx, sessionID, ipPtr, now, m.cfg.Session.IdleTimeout); err != nil {
			m.logFor(c).Warn("idle-session touch failed", zap.Error(err))
		} else if !active {
			m.abortError(c, errs.Unauthenticated().
				WithMeta("reason", "session has exceeded the idle timeout"))
			return
		}
		c.Next()
	}
}

// wwwAuthenticate maps an authentication error to the RFC 6750 challenge value.
// Expired tokens get the distinct error so a client knows to refresh rather than re-prompt for a password; other 401s are reported as a generic invalid_token to avoid leaking which check failed.
func wwwAuthenticate(err error) string {
	base := `Bearer realm="` + authRealm + `"`
	switch errs.CodeOf(err) {
	case errs.CodeTokenExpired:
		return base + `, error="invalid_token", error_description="the access token expired"`
	case errs.CodeForbidden, errs.CodeBranchScopeDenied, errs.CodeTenantScopeDenied:
		return base + `, error="insufficient_scope"`
	case errs.CodeUnauthenticated:
		return base
	default:
		return base + `, error="invalid_token"`
	}
}
