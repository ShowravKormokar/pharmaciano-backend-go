package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateTherapeuticClass inserts a therapeutic class.
//
// drug_class_id is optional. When supplied the operation is two statements — prove
// the class is live, then insert — so it runs in a transaction (contract §15).
// IsActive is resolved so an omitted value stores the column default TRUE (§28.5).
func (s *Service) CreateTherapeuticClass(ctx context.Context, req *CreateTherapeuticClassRequest) (*TherapeuticClass, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	t := &TherapeuticClass{
		Code:        req.Code,
		Name:        req.Name,
		DrugClassID: req.DrugClassID,
		Description: req.Description,
		IsActive:    boolOr(req.IsActive, true),
	}
	if req.DrugClassID == nil {
		return s.insertTherapeuticClass(ctx, t)
	}

	var out *TherapeuticClass
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := requireReference(ctx, req.DrugClassID, s.repo.GetDrugClassForUpdate, resDrugClass); err != nil {
			return err
		}
		created, err := s.repo.CreateTherapeuticClass(ctx, t)
		if err != nil {
			return mapWriteErr(err, resTherapeuticClass)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// insertTherapeuticClass is the single-statement create path for an unclassified
// therapeutic class.
func (s *Service) insertTherapeuticClass(ctx context.Context, t *TherapeuticClass) (*TherapeuticClass, error) {
	created, err := s.repo.CreateTherapeuticClass(ctx, t)
	if err != nil {
		return nil, mapWriteErr(err, resTherapeuticClass)
	}
	return created, nil
}

// GetTherapeuticClass returns one live therapeutic class, or NotFound.
func (s *Service) GetTherapeuticClass(ctx context.Context, id uuid.UUID) (*TherapeuticClass, error) {
	t, err := s.repo.GetTherapeuticClass(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resTherapeuticClass)
	}
	return t, nil
}

// ListTherapeuticClasses returns a filtered, paginated page of therapeutic classes
// plus meta. One of the three resources whose ?parent_id= filter is honoured,
// filtering on drug_class_id.
func (s *Service) ListTherapeuticClasses(ctx context.Context, q *ListMasterDataQuery) ([]TherapeuticClass, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListTherapeuticClasses)
}

// UpdateTherapeuticClass applies a partial change, including attaching or detaching
// a drug class, under a row lock in one transaction. An empty PATCH degrades to a
// read; code is immutable and not patchable.
func (s *Service) UpdateTherapeuticClass(ctx context.Context, id uuid.UUID, req *UpdateTherapeuticClassRequest) (*TherapeuticClass, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetTherapeuticClass(ctx, id)
	}

	var out *TherapeuticClass
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetTherapeuticClassForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resTherapeuticClass)
		}
		applyTherapeuticClassPatch(current, req)
		if err := requireReference(ctx, current.DrugClassID, s.repo.GetDrugClassForUpdate, resDrugClass); err != nil {
			return err
		}
		saved, err := s.repo.UpdateTherapeuticClass(ctx, current)
		if err != nil {
			return mapWriteErr(err, resTherapeuticClass)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteTherapeuticClass soft-deletes a therapeutic class.
//
// No medicine points at a therapeutic class directly (C-11), but generic medicines
// point at the classes BELOW it, and those are exactly the rows that would be
// detached. HasTherapeuticClassChildren is therefore the guard that closes the
// taxonomy chain at its last link.
func (s *Service) DeleteTherapeuticClass(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetTherapeuticClassForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resTherapeuticClass)
		}
		err := checkUnreferenced(ctx, resTherapeuticClass, id, s.repo.HasTherapeuticClassChildren)
		if err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteTherapeuticClass(ctx, id)
		if err != nil {
			return mapWriteErr(err, resTherapeuticClass)
		}
		if !ok {
			return errs.NotFound(resTherapeuticClass)
		}
		return nil
	})
}

// applyTherapeuticClassPatch copies the non-nil fields of the PATCH onto t.
func applyTherapeuticClassPatch(t *TherapeuticClass, req *UpdateTherapeuticClassRequest) {
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.DrugClassID != nil {
		t.DrugClassID = req.DrugClassID
	}
	if req.Description != nil {
		t.Description = req.Description
	}
	if req.IsActive != nil {
		t.IsActive = *req.IsActive
	}
}
