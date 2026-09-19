package warehouse

import (
	"context"
	"errors"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Service holds the warehouse business logic: tenant binding, the
// branch-ownership guard on the client-supplied branch_id, the single-main
// invariant and uniqueness translation. It is the only layer that maps raw
// driver errors to domain *errs.AppError values.
type Service struct {
	repo *Repository
	db   *db.DB
}

// NewService assembles the service from its repository and the shared database
// handle (needed for the transactions that keep multi-statement writes atomic).
func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database}
}

// Create inserts a warehouse under a branch of the caller's organization.
//
// branch_id comes from the request body, so the whole operation runs in one
// transaction that first proves the branch belongs to the caller's org — a
// foreign or unknown branch yields NotFound (never Forbidden), so a caller can't
// probe which branch ids exist in other tenants. If the warehouse is created as
// main, the "clear other mains" runs in the same transaction, so the branch is
// never transiently left with two main warehouses. A duplicate code within the
// branch becomes a 409.
func (s *Service) Create(ctx context.Context, req *CreateWarehouseRequest) (*Warehouse, error) {
	orgID := appctx.OrgID(ctx)

	w := &Warehouse{
		OrganizationID: orgID,
		BranchID:       req.BranchID,
		Code:           req.Code,
		Name:           req.Name,
		Location:       req.Location,
		Capacity:       req.Capacity,
		IsActive:       derefBool(req.IsActive, true), // default active
		IsMain:         derefBool(req.IsMain, false),  // default non-main
	}

	var out *Warehouse
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		ok, err := s.repo.BranchBelongsToOrg(ctx, orgID, w.BranchID)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if !ok {
			return errs.NotFound("branch")
		}
		// A branch-bound caller may only create a warehouse in one of their
		// assigned branches — never plant one in a branch outside their scope.
		if err := s.enforceBranchScope(ctx, w.BranchID); err != nil {
			return err
		}

		created, err := s.repo.Insert(ctx, w)
		if err != nil {
			return mapWriteErr(err)
		}
		if created.IsMain {
			if err := s.repo.ClearMainExcept(ctx, created.BranchID, created.ID); err != nil {
				return errs.DatabaseError(err)
			}
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns one warehouse in the caller's organization, or NotFound.
// A branch-bound caller additionally must be assigned to the warehouse's branch;
// a warehouse in an unassigned branch is reported as NotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Warehouse, error) {
	w, err := s.repo.FindByID(ctx, appctx.OrgID(ctx), id)
	if err != nil {
		return nil, mapReadErr(err)
	}
	if err := s.enforceBranchScope(ctx, w.BranchID); err != nil {
		return nil, err
	}
	return w, nil
}

// List returns a filtered, paginated page of the caller's warehouses plus meta.
// It is org-scoped, so a branch_id filter naming a branch outside the caller's
// org simply yields an empty page (no cross-tenant leak). A branch-bound caller
// is additionally pinned to their effective branch subset (BranchScope): a
// branch_id filter naming an unassigned branch yields an empty page — never
// another branch's warehouses (§84 scoped repository query).
func (s *Service) List(ctx context.Context, q *ListWarehousesQuery) ([]ListItem, pagination.Meta, error) {
	q.Offset.Normalize()
	filter := ListFilter{BranchID: q.BranchID, IsActive: q.IsActive, IsMain: q.IsMain}

	// Constrain a branch-bound caller to their assigned subset at the row level.
	if scope, orgWide := appctx.BranchScope(ctx); !orgWide {
		filter.BranchScope = scope
	}

	items, total, err := s.repo.List(ctx, appctx.OrgID(ctx), filter, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// Update applies a partial change. An empty request degrades to a read. If the
// request promotes the warehouse to main, the demotion of the previous main in
// the same branch happens in the same transaction. Runs under a row lock to
// avoid lost updates. code and branch_id are immutable and cannot be patched.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateWarehouseRequest) (*Warehouse, error) {
	orgID := appctx.OrgID(ctx)
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}

	var out *Warehouse
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}
		if err := s.enforceBranchScope(ctx, current.BranchID); err != nil {
			return err
		}
		applyPatch(current, req)
		if err := s.persist(ctx, current); err != nil {
			return err
		}
		out = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete soft-deletes a warehouse. Unlike a branch, a branch may have zero
// warehouses, so there is no last-warehouse guard. A branch-bound caller can
// only delete a warehouse in one of their assigned branches; the read and
// delete run in a single transaction so the warehouse cannot be moved between
// the scope check and the deletion.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	orgID := appctx.OrgID(ctx)
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		w, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}
		if err := s.enforceBranchScope(ctx, w.BranchID); err != nil {
			return err
		}
		if _, err := s.repo.SoftDelete(ctx, orgID, id); err != nil {
			return mapWriteErr(err)
		}
		return nil
	})
	return err
}

// persist writes current and, when it is now the main warehouse, clears the
// main flag on every other warehouse in the same branch — all within the
// caller's already-open tx.
func (s *Service) persist(ctx context.Context, current *Warehouse) error {
	saved, err := s.repo.Update(ctx, current)
	if err != nil {
		return mapWriteErr(err)
	}
	*current = *saved
	if saved.IsMain {
		if err := s.repo.ClearMainExcept(ctx, saved.BranchID, saved.ID); err != nil {
			return errs.DatabaseError(err)
		}
	}
	return nil
}

// applyPatch copies the non-nil fields of a PATCH onto w. Location and Capacity
// are pointer columns, so a set pointer (including one addressing an empty
// string / zero) overwrites, while nil leaves the column untouched.
func applyPatch(w *Warehouse, req *UpdateWarehouseRequest) {
	if req.Name != nil {
		w.Name = *req.Name
	}
	if req.Location != nil {
		w.Location = req.Location
	}
	if req.Capacity != nil {
		w.Capacity = req.Capacity
	}
	if req.IsActive != nil {
		w.IsActive = *req.IsActive
	}
	if req.IsMain != nil {
		w.IsMain = *req.IsMain
	}
}

// mapReadErr maps a repository read error: missing row → NotFound, else DB error.
func mapReadErr(err error) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound("warehouse")
	}
	return errs.DatabaseError(err)
}

// mapWriteErr maps a repository write error: a unique violation (duplicate code
// within the branch) → AlreadyExists; a missing row on UPDATE ... RETURNING →
// NotFound; anything else → DatabaseError.
func mapWriteErr(err error) error {
	switch {
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("warehouse")
	case db.IsUniqueVilation(err):
		return errs.AlreadyExists("warehouse code")
	default:
		return errs.DatabaseError(err)
	}
}

// derefBool returns *p if non-nil, otherwise def.
func derefBool(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

// enforceBranchScope returns nil when the caller is org-wide (no branch
// restriction) or when branchID is in the caller's assigned branch subset.
// A branch-bound caller requesting a resource in an unassigned branch gets
// CodeBranchScopeDenied — which the handler surfaces as 403 (resource exists
// but the caller lacks branch scope; never 404, which would leak existence).
func (s *Service) enforceBranchScope(ctx context.Context, branchID uuid.UUID) error {
	scope, orgWide := appctx.BranchScope(ctx)
	if orgWide {
		return nil
	}
	for _, b := range scope {
		if b == branchID {
			return nil
		}
	}
	return errs.New(errs.CodeBranchScopeDenied, "warehouse is outside your assigned branches")
}
