// Package-private scope resolution for Report. See REPORT_CONTRACT §4.2.
//
// A requested branch/warehouse is a selector, not authorization: it is
// intersected with the principal's current effective scope and never widens
// access. A stored scope snapshot is provenance, not a permanent grant.
package report

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
)

// ReadScope is the effective, tenant-scoped authorization boundary for a
// report request, execution, or download.
type ReadScope struct {
	OrganizationID uuid.UUID
	BranchIDs      []uuid.UUID // nil/empty = organization-wide
	OrgWide        bool
}

// ResolveScope derives the effective organization/branch scope from the
// trusted principal context and intersects an optional client-requested
// branch selector. A requested branch outside effective scope is denied; it
// never broadens access. A branch-bound principal with no scope is denied.
func ResolveScope(ctx context.Context, requestedBranches []uuid.UUID) (ReadScope, error) {
	if !appctx.IsAuthenticated(ctx) {
		return ReadScope{}, errs.Unauthenticated()
	}
	orgID := appctx.OrgID(ctx)
	if orgID == uuid.Nil {
		return ReadScope{}, errs.Forbidden("an active organization is required")
	}
	effective, orgWide := appctx.BranchScope(ctx)

	selected := make([]uuid.UUID, 0, len(requestedBranches))
	for _, b := range requestedBranches {
		if b == uuid.Nil {
			continue
		}
		selected = append(selected, b)
	}

	if len(selected) > 0 {
		if orgWide {
			// Org-wide principal with a selector: the org is bounded by
			// appctx.OrgID, so a foreign branch id can never enter here.
			return ReadScope{OrganizationID: orgID, BranchIDs: selected, OrgWide: false}, nil
		}
		// Branch-bound: must narrow to the assigned set; no client escalation.
		assigned := make(map[uuid.UUID]struct{}, len(effective))
		for _, b := range effective {
			assigned[b] = struct{}{}
		}
		intersected := make([]uuid.UUID, 0, len(selected))
		for _, b := range selected {
			if _, ok := assigned[b]; ok {
				intersected = append(intersected, b)
			}
		}
		if len(intersected) == 0 {
			return ReadScope{}, errs.Forbidden("requested branch is outside effective scope")
		}
		return ReadScope{OrganizationID: orgID, BranchIDs: intersected, OrgWide: false}, nil
	}

	if orgWide {
		return ReadScope{OrganizationID: orgID, OrgWide: true}, nil
	}
	if len(effective) == 0 {
		return ReadScope{}, errs.Forbidden("a valid branch scope is required")
	}
	branchIDs := make([]uuid.UUID, len(effective))
	copy(branchIDs, effective)
	return ReadScope{OrganizationID: orgID, BranchIDs: branchIDs, OrgWide: false}, nil
}

// String is a safe, non-sensitive representation for logs/audit.
func (s ReadScope) String() string {
	if s.OrgWide {
		return fmt.Sprintf("org=%s scope=org-wide", s.OrganizationID)
	}
	return fmt.Sprintf("org=%s branches=%d", s.OrganizationID, len(s.BranchIDs))
}
