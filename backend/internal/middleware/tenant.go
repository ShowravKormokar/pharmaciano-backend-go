package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"backend/internal/common/constants"
	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	uuidx "backend/internal/platform/uuid"
)

func (m *Middleware) Tenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		p, ok := appctx.CurrentPrincipal(ctx)
		if !ok {
			// Defensive: Tenant must be mounted after Auth. If it somehow runs
			// unauthenticated, fail closed rather than granting org-wide scope.
			m.abortError(c, errs.Unauthenticated().
				WithMeta("reason", "tenant scope requires an authenticated principal"))
			return
		}

		// Parse the optional branch selector.
		var requested *uuid.UUID
		if raw := c.GetHeader(constants.HeaderBranchScope); raw != "" {
			id, err := uuidx.Parse(raw)
			if err != nil {
				m.abortError(c, errs.Validation("invalid branch scope", errs.FieldError{
					Field:   constants.HeaderBranchScope,
					Rule:    "uuid",
					Message: "must be a valid branch id",
				}))
				return
			}
			requested = &id
		}

		orgWide := p.RoleName == constants.RoleSuperAdmin || p.RoleName == constants.RoleAdmin

		if orgWide {
			// Admins default to org-wide (nil) scope and may narrow to any branch
			// in their org via the header.
			if requested != nil {
				p.BranchID = requested
				c.Request = c.Request.WithContext(appctx.WithPrincipal(ctx, p))
			}
			c.Next()
			return
		}

		// Branch-bound principal: must have a home branch.
		if p.BranchID == nil {
			m.abortError(c, errs.New(errs.CodeBranchScopeDenied,
				"your account is not assigned to a branch").
				WithMeta("reason", "branch-bound principal has no assigned branch"))
			return
		}

		// A selector, if present, must match the home branch exactly.
		if requested != nil && *requested != *p.BranchID {
			m.abortError(c, errs.New(errs.CodeBranchScopeDenied,
				"you may only operate within your assigned branch").
				WithMeta("requested_branch", requested.String()).
				WithMeta("assigned_branch", p.BranchID.String()))
			return
		}

		// Effective branch is already the assigned one (set by Auth); nothing to
		// rewrite. Proceed.
		c.Next()
	}
}


// Tenant is the second link of the Protected() chain. It runs after Auth (so the
// Principal is on the context) and establishes the *effective branch scope* for
// the request, which every repository then folds into its WHERE clause.
//
// Isolation model:
//
//   - Organization (tenant) is bound to the token and can never be switched by a
//     request — there is no header for it. As long as every query filters by
//     appctx.OrgID, cross-tenant access is structurally impossible. This
//     middleware relies on that invariant rather than duplicating it.
//   - Branch scope is selectable within the caller's org via the X-Branch-ID
//     header:
//       * Org-wide roles (SUPER_ADMIN, ADMIN) may target any branch in their org,
//         or omit the header for org-wide access (effective branch = nil).
//       * Branch-bound roles are pinned to their assigned branch: they may pass
//         their own branch id or nothing, but requesting a *different* branch is
//         denied (BRANCH_SCOPE_DENIED). A branch-bound principal with no assigned
//         branch is a misconfiguration and is also denied.
//
// A selected branch is written back onto the Principal so downstream code reads
// the request's effective branch uniformly through appctx.BranchID. Whether that
// branch actually belongs to the caller's org is enforced by the data layer
// (every query is `WHERE org_id = ? [AND branch_id = ?]`): an id from another org
// simply matches no rows, so no cross-tenant data can leak even here.