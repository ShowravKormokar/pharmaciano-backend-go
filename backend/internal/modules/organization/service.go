package organization

import (
	"context"
	"errors"

	"github.com/google/uuid"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

// Service holds the organization business logic: tenant-scope enforcement,
// partial-update semantics and error translation. It sits between the handler
// (HTTP) and the repository (SQL) and is the only layer that maps raw driver
// errors to domain *errs.AppError values.
type Service struct {
	repo *Repository
	db   *db.DB
}

// NewService assembles the service from its repository and the shared database
// handle (needed to open the transaction that makes a partial update atomic).
func NewService(repo *Repository, database *db.DB) *Service {
	return &Service{repo: repo, db: database}
}

// GetCurrent returns the caller's own organization, resolved from the org id the
// auth/tenant middleware bound to the context. It never trusts a client-supplied
// id, so cross-tenant access is impossible by construction.
func (s *Service) GetCurrent(ctx context.Context) (*Organization, error) {
	return s.fetch(ctx, appctx.OrgID(ctx))
}

// GetByID returns the organization with the given id. A super-admin may read any
// organization; every other caller may only read their own — a mismatch yields
// NotFound (never Forbidden) so the endpoint cannot be used to probe whether
// another tenant's organization exists.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*Organization, error) {
	if err := authorizeScope(ctx, id); err != nil {
		return nil, err
	}
	return s.fetch(ctx, id)
}

// Update applies a partial change to an organization's profile and returns the
// refreshed row. Scope is enforced exactly as in GetByID. An empty request is a
// no-op that degrades to a plain read. The read-modify-write runs inside a
// transaction with SELECT ... FOR UPDATE so concurrent PATCHes cannot lose
// each other's fields.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *UpdateOrganizationRequest) (*Organization, error) {
	if err := authorizeScope(ctx, id); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.fetch(ctx, id)
	}

	var out *Organization
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindByIDForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err)
		}
		applyUpdate(current, req)
		updated, err := s.repo.Update(ctx, current)
		if err != nil {
			return mapReadErr(err)
		}
		out = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Summary returns dashboard counters (branch count, user count) plus the current
// plan/activation for an organization. Scope is enforced exactly as in GetByID.
// The three reads run in one read-only transaction so the counts are a
// consistent snapshot rather than three independently-timed queries.
func (s *Service) Summary(ctx context.Context, id uuid.UUID) (*SummaryResponse, error) {
	if err := authorizeScope(ctx, id); err != nil {
		return nil, err
	}

	var out SummaryResponse
	err := s.db.WithTxReadOnly(ctx, func(ctx context.Context) error {
		org, err := s.repo.FindByID(ctx, id)
		if err != nil {
			return mapReadErr(err)
		}
		branches, err := s.repo.CountBranches(ctx, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		users, err := s.repo.CountUsers(ctx, id)
		if err != nil {
			return errs.DatabaseError(err)
		}
		out = SummaryResponse{
			OrganizationID:   org.ID,
			Name:             org.Name,
			SubscriptionPlan: org.SubscriptionPlan,
			IsActive:         org.IsActive,
			BranchCount:      branches,
			UserCount:        users,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// fetch reads one organization and maps a missing/deleted row to NotFound.
func (s *Service) fetch(ctx context.Context, id uuid.UUID) (*Organization, error) {
	org, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, mapReadErr(err)
	}
	return org, nil
}

// authorizeScope enforces tenant isolation: unless the caller is a super-admin,
// the addressed id must equal the caller's own organization. A mismatch returns
// NotFound rather than Forbidden so the endpoint never confirms the existence of
// another tenant's organization (fail closed, no cross-tenant probing).
func authorizeScope(ctx context.Context, id uuid.UUID) error {
	if appctx.IsSuperAdmin(ctx) {
		return nil
	}
	if id != appctx.OrgID(ctx) {
		return errs.NotFound("organization")
	}
	return nil
}

// mapReadErr translates a repository read error: a missing row becomes a domain
// NotFound, anything else a DatabaseError (logged and served as a generic 500).
func mapReadErr(err error) error {
	if errors.Is(err, db.ErrNoRows) {
		return errs.NotFound("organization")
	}
	return errs.DatabaseError(err)
}

// applyUpdate copies the non-nil fields of req onto o. A nil pointer leaves the
// column unchanged; a non-nil pointer overwrites it (an empty string blanks a
// free-text column but cannot set SQL NULL — see UpdateOrganizationRequest).
func applyUpdate(o *Organization, req *UpdateOrganizationRequest) {
	if req.Name != nil {
		o.Name = *req.Name
	}
	if req.TradeLicenseNo != nil {
		o.TradeLicenseNo = req.TradeLicenseNo
	}
	if req.DrugLicenseNo != nil {
		o.DrugLicenseNo = req.DrugLicenseNo
	}
	if req.VATRegistrationNo != nil {
		o.VATRegistrationNo = req.VATRegistrationNo
	}
	if req.TIN != nil {
		o.TIN = req.TIN
	}
	if req.ContactPhone != nil {
		o.ContactPhone = req.ContactPhone
	}
	if req.ContactEmail != nil {
		o.ContactEmail = req.ContactEmail
	}
	if req.Website != nil {
		o.Website = req.Website
	}
	if req.LogoURL != nil {
		o.LogoURL = req.LogoURL
	}
	if req.AddressLine1 != nil {
		o.AddressLine1 = req.AddressLine1
	}
	if req.AddressLine2 != nil {
		o.AddressLine2 = req.AddressLine2
	}
	if req.City != nil {
		o.City = req.City
	}
	if req.State != nil {
		o.State = req.State
	}
	if req.PostalCode != nil {
		o.PostalCode = req.PostalCode
	}
	if req.Country != nil {
		o.Country = req.Country
	}
	if req.Currency != nil {
		o.Currency = *req.Currency
	}
	if req.Timezone != nil {
		o.Timezone = *req.Timezone
	}
}
