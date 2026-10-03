package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateTaxRate inserts a tax rate.
//
// rate_percent is NOT NULL with no schema default and CreateTaxRate writes every
// non-generated column, so a nil pointer here would be a nil insert against a NOT
// NULL column — a driver error, and a 500 for what is really a bad request. It is
// therefore rejected explicitly with a 400 before the repository is ever called.
// (The DTO marks it `required` too; this is the same check reachable without the
// handler, and it costs one branch.)
//
// IsDefault and IsActive are resolved to the schema defaults FALSE and TRUE
// respectively, so an omitted flag stores the column default rather than a zero
// value (§28.5).
//
// SINGLE DEFAULT IS NOT ENFORCED — deliberately. Contract C-08 and §11.2 record
// "exactly one is_default tax rate" as UNRESOLVED: there is no unique constraint on
// the column (000005:94) and no decided rule for which row wins a conflict. The
// owner confirmed during Phase 05 that this phase must not invent one, so this
// method stores is_default exactly as asked and the schema continues to permit
// several. Enforcing it later needs a clear-other-defaults repository write path
// and a partial unique index, neither of which exists.
func (s *Service) CreateTaxRate(ctx context.Context, req *CreateTaxRateRequest) (*TaxRate, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.RatePercent == nil {
		return nil, errs.Validation("rate_percent is required",
			errs.FieldError{
				Field:   "rate_percent",
				Rule:    "required",
				Message: "a tax rate must have a percentage, and 0 is a legal value",
			})
	}
	if err := s.requireRatePercent(*req.RatePercent); err != nil {
		return nil, err
	}

	t := &TaxRate{
		Code:        req.Code,
		Name:        req.Name,
		RatePercent: *req.RatePercent,
		IsDefault:   boolOr(req.IsDefault, false),
		IsActive:    boolOr(req.IsActive, true),
	}
	created, err := s.repo.CreateTaxRate(ctx, t)
	if err != nil {
		return nil, mapWriteErr(err, resTaxRate)
	}
	return created, nil
}

// GetTaxRate returns one live tax rate, or NotFound.
func (s *Service) GetTaxRate(ctx context.Context, id uuid.UUID) (*TaxRate, error) {
	t, err := s.repo.GetTaxRate(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resTaxRate)
	}
	return t, nil
}

// ListTaxRates returns a filtered, paginated page of tax rates plus meta.
//
// is_default and rate_percent are sortable; ordering by rate_percent is how a client
// finds the cheapest applicable rate without loading the whole catalogue.
func (s *Service) ListTaxRates(ctx context.Context, q *ListMasterDataQuery) ([]TaxRate, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListTaxRates)
}

// UpdateTaxRate applies a partial change under a row lock in one transaction.
//
// rate_percent is validated when present but, as on create, the single-default rule
// is NOT enforced: see CreateTaxRate. Setting is_default=true here simply makes this
// row a default alongside any others, which is what the schema currently permits.
func (s *Service) UpdateTaxRate(ctx context.Context, id uuid.UUID, req *UpdateTaxRateRequest) (*TaxRate, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetTaxRate(ctx, id)
	}
	if req.RatePercent != nil {
		if err := s.requireRatePercent(*req.RatePercent); err != nil {
			return nil, err
		}
	}

	var out *TaxRate
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetTaxRateForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resTaxRate)
		}
		applyTaxRatePatch(current, req)
		saved, err := s.repo.UpdateTaxRate(ctx, current)
		if err != nil {
			return mapWriteErr(err, resTaxRate)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteTaxRate soft-deletes a tax rate, refusing while a live medicine still names
// it (D-19).
func (s *Service) DeleteTaxRate(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetTaxRateForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resTaxRate)
		}
		if err := checkUnreferenced(ctx, resTaxRate, id, s.repo.IsTaxRateReferenced); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteTaxRate(ctx, id)
		if err != nil {
			return mapWriteErr(err, resTaxRate)
		}
		if !ok {
			return errs.NotFound(resTaxRate)
		}
		return nil
	})
}

// applyTaxRatePatch copies the non-nil fields of the PATCH onto t.
func applyTaxRatePatch(t *TaxRate, req *UpdateTaxRateRequest) {
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.RatePercent != nil {
		t.RatePercent = *req.RatePercent
	}
	if req.IsDefault != nil {
		t.IsDefault = *req.IsDefault
	}
	if req.IsActive != nil {
		t.IsActive = *req.IsActive
	}
}
