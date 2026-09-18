package branch

import (
	"context"
	"errors"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Service holds the branch business logic: tenant binding, the single-default
// invariant, uniqueness translation and the last-branch guard. It is the only
// layer that maps raw driver errors to domain *errs.AppError values.
type Service struct {
	repo *Repository
	db   *db.DB
}

// NewService assembles the service from its repository and the shared database
// handle (needed for the transactions that keep multi-statement writes atomic).
func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database}
}

// Create inserts a new branch in the caller's organization. If the request marks
// it default, the create and the "clear other defaults" run in one transaction
// so the org never transiently has two defaults. A duplicate code becomes a 409.
func (s *Service) Create(ctx context.Context, req *CreateBranchRequest) (*Branch, error) {
	orgID := appctx.OrgID(ctx)

	b := &Branch{
		OrganizationID: orgID,
		Code:           req.Code,
		Name:           req.Name,
		IsActive:       derefBool(req.IsActive, true),   // default active
		IsDefault:      derefBool(req.IsDefault, false), // default non-default
		Address:        req.Address,
		City:           req.City,
		State:          req.State,
		PostalCode:     req.PostalCode,
		Country:        req.Country,
		Email:          req.Email,
		Phone:          req.Phone,
		Latitude:       req.Latitude,
		Longitude:      req.Longitude,
		OpenTime:       req.OpenTime,
		CloseTime:      req.CloseTime,
	}

	var out *Branch
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		created, err := s.repo.Insert(ctx, b)
		if err != nil {
			return mapWriteErr(err)
		}
		if created.IsDefault {
			if err := s.repo.ClearDefaultExcept(ctx, orgID, created.ID); err != nil {
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

// Get returns one branch in the caller's organization, or NotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Branch, error) {
	b, err := s.repo.FindByID(ctx, appctx.OrgID(ctx), id)
	if err != nil {
		return nil, mapReadErr(err)
	}
	return b, nil
}

// List returns a filtered, paginated page of the caller's branches plus meta.
func (s *Service) List(ctx context.Context, q *ListBranchesQuery) ([]ListItem, pagination.Meta, error) {
	q.Offset.Normalize()
	filter := ListFilter{IsActive: q.IsActive, City: q.City, Q: q.Q}

	items, total, err := s.repo.List(ctx, appctx.OrgID(ctx), filter, q.Offset)
	if err != nil {
		return nil, pagination.Meta{}, errs.DatabaseError(err)
	}
	return items, pagination.BuildOffsetMeta(q.Offset, total), nil
}

// Update applies a partial change. An empty request degrades to a read. If the
// request promotes the branch to default, the demotion of the previous default
// happens in the same transaction. Runs under a row lock to avoid lost updates.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateBranchRequest) (*Branch, error) {
	orgID := appctx.OrgID(ctx)
	if req.IsEmpty() {
		return s.Get(ctx, id)
	}

	var out *Branch
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}
		applyPatch(current, req)
		if err := s.persist(ctx, orgID, current); err != nil {
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

// Replace performs a full update (PUT): the resulting row is exactly the request
// (code and organization_id excepted, which stay fixed). Optional fields absent
// from the request become NULL. Same transactional default handling as Update.
func (s *Service) Replace(ctx context.Context, id uuid.UUID, req *ReplaceBranchRequest) (*Branch, error) {
	orgID := appctx.OrgID(ctx)

	var out *Branch
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(ctx, orgID, id)
		if err != nil {
			return mapReadErr(err)
		}
		applyReplace(current, req)
		if err := s.persist(ctx, orgID, current); err != nil {
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

// Delete soft-deletes a branch. It refuses to remove the organization's last
// remaining branch (an org must always have at least one) with a 409, so the
// tenant can never be left with nowhere to attribute users and stock.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	orgID := appctx.OrgID(ctx)

	return s.db.WithTx(ctx, func(ctx context.Context) error {
		// Lock the row so a concurrent delete/update can't race the count.
		if _, err := s.repo.FindByIDForUpdate(ctx, orgID, id); err != nil {
			return mapReadErr(err)
		}
		remaining, err := s.repo.CountExcept(ctx, orgID, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		if remaining == 0 {
			return errs.Conflict("cannot delete the organization's only branch")
		}
		ok, err := s.repo.SoftDelete(ctx, orgID, id)
		if err != nil {
			return mapWriteErr(err)
		}
		if !ok {
			return errs.NotFound("branch")
		}
		return nil
	})
}

// persist writes current and, when it is now the default, clears the default
// flag on every other branch — all within the caller's already-open tx.
func (s *Service) persist(ctx context.Context, orgID uuid.UUID, current *Branch) error {
	saved, err := s.repo.Update(ctx, current)
	if err != nil {
		return mapWriteErr(err)
	}
	*current = *saved
	if saved.IsDefault {
		if err := s.repo.ClearDefaultExcept(ctx, orgID, saved.ID); err != nil {
			return errs.DatabaseError(err)
		}
	}
	return nil
}

// applyPatch copies the non-nil fields of a PATCH onto b.
func applyPatch(b *Branch, req *UpdateBranchRequest) {
	if req.Name != nil {
		b.Name = *req.Name
	}
	if req.IsActive != nil {
		b.IsActive = *req.IsActive
	}
	if req.IsDefault != nil {
		b.IsDefault = *req.IsDefault
	}
	if req.Address != nil {
		b.Address = req.Address
	}
	if req.City != nil {
		b.City = req.City
	}
	if req.State != nil {
		b.State = req.State
	}
	if req.PostalCode != nil {
		b.PostalCode = req.PostalCode
	}
	if req.Country != nil {
		b.Country = req.Country
	}
	if req.Email != nil {
		b.Email = req.Email
	}
	if req.Phone != nil {
		b.Phone = req.Phone
	}
	if req.Latitude != nil {
		b.Latitude = req.Latitude
	}
	if req.Longitude != nil {
		b.Longitude = req.Longitude
	}
	if req.OpenTime != nil {
		b.OpenTime = req.OpenTime
	}
	if req.CloseTime != nil {
		b.CloseTime = req.CloseTime
	}
}

// applyReplace overwrites every mutable field of b from a PUT — including
// setting optional fields to nil when the request omitted them.
func applyReplace(b *Branch, req *ReplaceBranchRequest) {
	b.Name = req.Name
	b.IsActive = req.IsActive
	b.IsDefault = req.IsDefault
	b.Address = req.Address
	b.City = req.City
	b.State = req.State
	b.PostalCode = req.PostalCode
	b.Country = req.Country
	b.Email = req.Email
	b.Phone = req.Phone
	b.Latitude = req.Latitude
	b.Longitude = req.Longitude
	b.OpenTime = req.OpenTime
	b.CloseTime = req.CloseTime
}

// mapReadErr maps a repository read error: missing row → NotFound, else DB error.
func mapReadErr(err error) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound("branch")
	}
	return errs.DatabaseError(err)
}

// mapWriteErr maps a repository write error: a unique violation (duplicate code
// within the org) → AlreadyExists; a missing row on UPDATE ... RETURNING →
// NotFound; anything else → DatabaseError.
func mapWriteErr(err error) error {
	switch {
	case errors.Is(err, db.ErrNoRows):
		return errs.NotFound("branch")
	case db.IsUniqueVilation(err):
		return errs.AlreadyExists("branch code")
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
