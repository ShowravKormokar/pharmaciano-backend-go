package supplier

import (
	"context"
	"errors"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Service holds the supplier business logic: tenant binding, code uniqueness
// translation, credit-term null semantics and branch/delegation guards.
// It is the only layer that maps raw driver errors to domain *errs.AppError values.
type Service struct {
	repo *Repository
	db   *db.DB
	mapper *Mapper
}

// NewService assembles the service from its repository and the shared database
// handle.
func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database, mapper: NewMapper()}
}

// Create inserts a supplier under the caller's organization.
//
// organization_id is taken from the trusted authenticated context, never the
// body (contract §6). Opening/current balances are not accepted from the
// payload and are not set here — they are Ledger-owned.
func (s *Service) Create(ctx context.Context, req *CreateSupplierRequest) (*SupplierResponse, error) {
	orgID := appctx.OrgID(ctx)

	sup := s.mapper.ToModel(req)
	sup.OrganizationID = orgID.String()

	var out *Supplier
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		created, err := s.repo.Insert(ctx, sup)
		if err != nil {
			return mapWriteErr(err)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.mapper.ToResponse(out), nil
}

// Get returns one supplier in the caller's organization, or NotFound.
// Cross-tenant reads are indistinguishable from missing.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*SupplierResponse, error) {
	orgID := appctx.OrgID(ctx)
	sup, err := s.repo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, mapReadErr(err)
	}
	return s.mapper.ToResponse(sup), nil
}

// List returns a filtered, paginated page of the caller's suppliers plus meta.
// Org-scoped only (contract §4.5).
func (s *Service) List(ctx context.Context, q *ListSuppliersQuery) ([]ListItem, pagination.Meta, error) {
	q.Offset.Normalize()
	filter := ListFilter{City: q.City, IsActive: q.IsActive, Q: q.NormalizedQ()}

	items, total, err := s.repo.List(ctx, appctx.OrgID(ctx), filter, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// Update applies a partial change. An empty request degrades to a read.
// Code is immutable; balances and organization_id cannot be patched.
// Runs under a row lock to avoid lost updates.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateSupplierRequest) (*SupplierResponse, error) {
	orgID := appctx.OrgID(ctx)
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}

	var out *Supplier
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}
		s.mapper.ApplyPatch(current, req)
		saved, err := s.repo.Update(ctx, current)
		if err != nil {
			return mapWriteErr(err)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.mapper.ToResponse(out), nil
}

// Delete archives/inactivates a supplier (soft-delete). Never physical delete.
// Purchases and inventory rows reference suppliers; preserving identity is
// required (contract §13).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	orgID := appctx.OrgID(ctx)
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		ok, err := s.repo.SoftDelete(ctx, orgID, id)
		if err != nil {
			return mapWriteErr(err)
		}
		if !ok {
			return errs.NotFound("supplier")
		}
		return nil
	})
	return err
}

// mapReadErr maps a repository read error: missing row → NotFound, else DB error.
func mapReadErr(err error) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound("supplier")
	}
	return errs.DatabaseError(err)
}

// mapWriteErr maps a repository write error: a unique violation (duplicate code)
// → AlreadyExists; a missing row on UPDATE ... RETURNING → NotFound; anything
// else → DatabaseError.
func mapWriteErr(err error) error {
	switch {
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("supplier")
	case db.IsUniqueVilation(err):
		return errs.AlreadyExists("supplier code")
	default:
		return errs.DatabaseError(err)
	}
}