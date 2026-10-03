package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateDosageForm inserts a dosage form.
//
// A route_id is optional. When one is supplied the operation is two statements —
// prove the route is live, then insert — so it runs in a transaction (contract §15);
// otherwise it is a single INSERT with no transaction.
//
// IsActive is resolved before the call because CreateDosageForm writes every
// non-generated column, so an unresolved nil would store false instead of the column
// default TRUE (§28.5).
func (s *Service) CreateDosageForm(ctx context.Context, req *CreateDosageFormRequest) (*DosageForm, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	f := &DosageForm{
		Code:        req.Code,
		Name:        req.Name,
		RouteID:     req.RouteID,
		Description: req.Description,
		IsActive:    boolOr(req.IsActive, true),
	}
	if req.RouteID == nil {
		return s.insertDosageForm(ctx, f)
	}

	var out *DosageForm
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := requireReference(ctx, req.RouteID, s.repo.GetRoute, resRoute); err != nil {
			return err
		}
		created, err := s.repo.CreateDosageForm(ctx, f)
		if err != nil {
			return mapWriteErr(err, resDosageForm)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// insertDosageForm is the single-statement create path, shared by the route-less
// case above.
func (s *Service) insertDosageForm(ctx context.Context, f *DosageForm) (*DosageForm, error) {
	created, err := s.repo.CreateDosageForm(ctx, f)
	if err != nil {
		return nil, mapWriteErr(err, resDosageForm)
	}
	return created, nil
}

// GetDosageForm returns one live dosage form, or NotFound.
func (s *Service) GetDosageForm(ctx context.Context, id uuid.UUID) (*DosageForm, error) {
	f, err := s.repo.GetDosageForm(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resDosageForm)
	}
	return f, nil
}

// ListDosageForms returns a filtered, paginated page of dosage forms plus meta.
//
// dosage_forms.route_id is indexed but deliberately not filterable: the API
// contract defines no ?route_id= parameter and inventing query syntax is out of
// scope (contract §28.4).
func (s *Service) ListDosageForms(ctx context.Context, q *ListMasterDataQuery) ([]DosageForm, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListDosageForms)
}

// UpdateDosageForm applies a partial change, including attaching or detaching a
// route, under a row lock in one transaction. An empty PATCH degrades to a read;
// code is immutable and not patchable.
func (s *Service) UpdateDosageForm(ctx context.Context, id uuid.UUID, req *UpdateDosageFormRequest) (*DosageForm, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetDosageForm(ctx, id)
	}

	var out *DosageForm
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetDosageFormForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resDosageForm)
		}
		applyDosageFormPatch(current, req)
		if err := requireReference(ctx, current.RouteID, s.repo.GetRoute, resRoute); err != nil {
			return err
		}
		saved, err := s.repo.UpdateDosageForm(ctx, current)
		if err != nil {
			return mapWriteErr(err, resDosageForm)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteDosageForm soft-deletes a dosage form, refusing while a live medicine still
// names it (D-19).
func (s *Service) DeleteDosageForm(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetDosageFormForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resDosageForm)
		}
		if err := checkUnreferenced(ctx, resDosageForm, id, s.repo.IsDosageFormReferenced); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteDosageForm(ctx, id)
		if err != nil {
			return mapWriteErr(err, resDosageForm)
		}
		if !ok {
			return errs.NotFound(resDosageForm)
		}
		return nil
	})
}

// applyDosageFormPatch copies the non-nil fields of the PATCH onto f.
func applyDosageFormPatch(f *DosageForm, req *UpdateDosageFormRequest) {
	if req.Name != nil {
		f.Name = *req.Name
	}
	if req.RouteID != nil {
		f.RouteID = req.RouteID
	}
	if req.Description != nil {
		f.Description = req.Description
	}
	if req.IsActive != nil {
		f.IsActive = *req.IsActive
	}
}
