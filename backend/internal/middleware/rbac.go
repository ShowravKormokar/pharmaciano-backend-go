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

		// Every decision is server-side; claims never contain permissions.
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
// The injected Authorizer resolves current server-side policy. Its cache is
// versioned by the authenticated subject's authorization epoch, so a mutation
// cannot be revived by an old JWT permission snapshot.
