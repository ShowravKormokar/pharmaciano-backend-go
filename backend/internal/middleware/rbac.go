package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
)

func (m *Middleware) RBAC(module, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Tell a downstream Audit entry exactly what this route touches, no matter which branch below grants access.
		c.Set(ginKeyAuditModule, module)
		c.Set(ginKeyAuditAction, action)

		// 1) Break-glass role.
		if appctx.IsSuperAdmin(ctx) {
			c.Next()
			return
		}

		// 2) Embedded-permission fast path.
		if appctx.HasPermission(ctx, module, action) {
			c.Next()
			return
		}

		// 3) Authoritative enforcement, scoped to the caller's org (domain).
		sub := appctx.UserID(ctx).String()
		dom := appctx.OrgID(ctx).String()
		allowed, err := m.authz.Enforce(ctx, sub, dom, module, action)
		if err != nil {
			m.logFor(c).Error("authorization enforcement failed; denying",
				zap.String("module", module),
				zap.String("action", action),
				zap.Error(err),
			)
			m.abortError(c, errs.Forbidden("authorization could not be verified").
				WithMeta("reason", "enforcer error").
				WithCause(err))
			return
		}
		if !allowed {
			m.abortError(c, errs.Forbidden("").
				WithMeta("module", module).
				WithMeta("action", action))
			return
		}

		c.Next()
	}
}

// RBAC is the third and final link of the Protected() chain. Given the
// module:action a route requires, it decides whether the current Principal may
// proceed. It runs after Tenant, so both identity and effective org/branch scope
// are already on the context.
//
// Decision order (first match wins):
//
//  1. SUPER_ADMIN short-circuit. The platform break-glass role bypasses
//     per-permission checks by design; its every action is still access-logged
//     and (for mutations) audited.
//  2. Embedded-permission fast path. The Principal carries the permission set the
//     Authenticator loaded from the (near-real-time) session/permission cache;
//     HasPermission resolves exact, module:* and global "*" grants without a
//     round-trip. This keeps the hot path allocation- and I/O-free for the
//     overwhelming majority of requests.
//  3. Authoritative enforcer. Anything not covered by the flat permission set
//     falls through to the injected Authorizer (Casbin RBAC-with-domains, where
//     the domain is the org id), which can express policies the embedded list
//     cannot (e.g. inherited roles, resource-scoped rules).
//
// Fail-closed: an enforcer error is treated as a denial (never open), and the
// default (unwired) Authorizer denies everything. Because the embedded set is
// refreshed per request by the Authenticator, a revoked permission stops
// granting access as soon as the session cache is invalidated — the fast path
// never becomes a stale back door.
