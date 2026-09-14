package middleware

import (
	"strings"

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

		// Parse the optional branch selector. ADR §19 supports TWO shapes:
		//   - X-Branch-ID  (legacy, single id) — backward compatible.
		//   - X-Branch-IDs (multi, comma-separated) — multi-branch subset.
		requested := m.parseBranchSelector(c)

		assigned := p.BranchIDs
		orgWide := p.RoleName == constants.RoleSuperAdmin || p.RoleName == constants.RoleAdmin

		if orgWide {
			// Admins default to org-wide (nil) scope and may narrow to any
			// subset of the org's branches via either header. The X-Branch-ID
			// legacy form coerces to a single-element slice so the rest of
			// the pipeline can stay slice-only.
			if len(requested) > 0 {
				p.BranchID = &requested[0]
				p.BranchIDs = requested
				c.Request = c.Request.WithContext(appctx.WithPrincipal(ctx, p))
			}
			c.Next()
			return
		}

		// Branch-bound principal: must have at least one assigned branch.
		if len(assigned) == 0 {
			m.abortError(c, errs.New(errs.CodeBranchScopeDenied,
				"your account is not assigned to any branch").
				WithMeta("reason", "branch-bound principal has no assigned branch"))
			return
		}

		// No selector: the principal's full assigned subset is the request's
		// effective scope.
		if len(requested) == 0 {
			p.BranchID = &assigned[0]
			p.BranchIDs = assigned
			c.Request = c.Request.WithContext(appctx.WithPrincipal(ctx, p))
			c.Next()
			return
		}

		// A selector was provided: it MUST be a subset of assigned. Defense in
		// depth: a stolen token + hostile client cannot widen the scope.
		intersected := intersectBranches(assigned, requested)
		if len(intersected) == 0 {
			m.observeSecurityDenial("branch_scope_denied")
			m.abortError(c, errs.New(errs.CodeBranchScopeDenied,
				"requested branch set is not within your assigned branches").
				WithMeta("requested_count", len(requested)).
				WithMeta("assigned_count", len(assigned)))
			return
		}
		p.BranchID = &intersected[0]
		p.BranchIDs = intersected
		c.Request = c.Request.WithContext(appctx.WithPrincipal(ctx, p))
		c.Next()
	}
}

// parseBranchSelector parses the optional X-Branch-IDs (multi) and the
// legacy X-Branch-ID (single) headers, returning a deduplicated slice of
// branch ids. The X-Branch-IDs header wins when both are present (the
// caller has signalled they understand the new shape). Empty/invalid
// values are skipped with a 400; a header that resolves to an empty set is
// treated as "no selector" so an unset and an empty list behave the same.
func (m *Middleware) parseBranchSelector(c *gin.Context) []uuid.UUID {
	if raw := strings.TrimSpace(c.GetHeader(constants.HeaderBranchIDs)); raw != "" {
		parts := strings.Split(raw, ",")
		out := make([]uuid.UUID, 0, len(parts))
		seen := make(map[uuid.UUID]struct{}, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := uuidx.Parse(p)
			if err != nil {
				// Reject the whole request: a partially-valid selector is a
				// client bug we should surface rather than silently truncate.
				m.abortError(c, errs.Validation("invalid branch selector", errs.FieldError{
					Field:   constants.HeaderBranchIDs,
					Rule:    "uuid",
					Message: "every value must be a valid branch id",
				}))
				return nil
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		return out
	}
	if raw := strings.TrimSpace(c.GetHeader(constants.HeaderBranchScope)); raw != "" {
		id, err := uuidx.Parse(raw)
		if err != nil {
			m.abortError(c, errs.Validation("invalid branch scope", errs.FieldError{
				Field:   constants.HeaderBranchScope,
				Rule:    "uuid",
				Message: "must be a valid branch id",
			}))
			return nil
		}
		return []uuid.UUID{id}
	}
	return nil
}

// intersectBranches returns every id in want that is also in have,
// preserving the order of want.
func intersectBranches(have, want []uuid.UUID) []uuid.UUID {
	if len(have) == 0 || len(want) == 0 {
		return nil
	}
	idx := make(map[uuid.UUID]struct{}, len(have))
	for _, h := range have {
		idx[h] = struct{}{}
	}
	out := make([]uuid.UUID, 0, len(want))
	for _, w := range want {
		if _, ok := idx[w]; ok {
			out = append(out, w)
		}
	}
	return out
}

// observeSecurityDenial is a nil-safe forward to the security_denials_total
// counter so the tenant middleware can record branch-scope denials without
// carrying a *Metrics field on Middleware.
func (m *Middleware) observeSecurityDenial(reason string) {
	if m == nil || m.metrics == nil {
		return
	}
	m.metrics.ObserveSecurityDenial(reason)
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
//     (legacy single) or X-Branch-IDs (multi-subset) headers:
//       * Org-wide roles (SUPER_ADMIN, ADMIN) may target any subset of the
//         org's branches, or omit the header for org-wide access (effective
//         branch = nil).
//       * Branch-bound roles are pinned to the subset baked into their JWT
//         (BranchIDs, ADR §19): they may pass a subset of it, or nothing,
//         but requesting a branch outside it is denied
//         (BRANCH_SCOPE_DENIED).
//
// A selected branch is written back onto the Principal (BranchID is the first
// element for legacy callers; BranchIDs is the full effective slice) so
// downstream code reads the request's effective scope uniformly through
// appctx.BranchID / appctx.BranchIDs. Whether those branches actually belong
// to the caller's org is enforced by the data layer (every query is
// `WHERE org_id = ? [AND branch_id = ANY($branches)]`): an id from another
// org simply matches no rows, so no cross-tenant data can leak even here.