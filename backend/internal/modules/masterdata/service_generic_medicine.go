package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateGenericMedicine inserts a generic medicine.
//
// therapeutic_class_id is optional. When supplied the operation is two statements —
// prove the class is live, then insert — so it runs in a transaction (contract §15).
//
// IsControlled and IsActive are resolved to the schema defaults FALSE and TRUE, so an
// omitted flag stores the column default rather than a zero value (§28.5).
func (s *Service) CreateGenericMedicine(ctx context.Context, req *CreateGenericMedicineRequest) (*GenericMedicine, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	g := &GenericMedicine{
		Code:               req.Code,
		Name:               req.Name,
		Description:        req.Description,
		TherapeuticClassID: req.TherapeuticClassID,
		IsControlled:       boolOr(req.IsControlled, false),
		IsActive:           boolOr(req.IsActive, true),
	}
	if req.TherapeuticClassID == nil {
		return s.insertGenericMedicine(ctx, g)
	}

	var out *GenericMedicine
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := requireReference(ctx, req.TherapeuticClassID, s.repo.GetTherapeuticClassForUpdate, resTherapeuticClass); err != nil {
			return err
		}
		created, err := s.repo.CreateGenericMedicine(ctx, g)
		if err != nil {
			return mapWriteErr(err, resGenericMedicine)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// insertGenericMedicine is the single-statement create path for an unclassified
// generic medicine.
func (s *Service) insertGenericMedicine(ctx context.Context, g *GenericMedicine) (*GenericMedicine, error) {
	created, err := s.repo.CreateGenericMedicine(ctx, g)
	if err != nil {
		return nil, mapWriteErr(err, resGenericMedicine)
	}
	return created, nil
}

// GetGenericMedicine returns one live generic medicine, or NotFound.
func (s *Service) GetGenericMedicine(ctx context.Context, id uuid.UUID) (*GenericMedicine, error) {
	g, err := s.repo.GetGenericMedicine(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resGenericMedicine)
	}
	return g, nil
}

// ListGenericMedicines returns a filtered, paginated page of generic medicines plus
// meta.
//
// generic_medicines.therapeutic_class_id is indexed but not filterable: the API
// contract defines no parameter for it (contract §28.4).
func (s *Service) ListGenericMedicines(ctx context.Context, q *ListMasterDataQuery) ([]GenericMedicine, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListGenericMedicines)
}

// UpdateGenericMedicine applies a partial change, including classifying or
// unclassifying, under a row lock in one transaction. An empty PATCH degrades to a
// read; code is immutable and not patchable.
//
// is_controlled is a plain flag with no derived rules: the service records it and
// does not police it, because what "controlled" legally restricts has no definition
// anywhere in this repository.
func (s *Service) UpdateGenericMedicine(ctx context.Context, id uuid.UUID, req *UpdateGenericMedicineRequest) (*GenericMedicine, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetGenericMedicine(ctx, id)
	}

	var out *GenericMedicine
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetGenericMedicineForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resGenericMedicine)
		}
		applyGenericMedicinePatch(current, req)
		if err := requireReference(ctx, current.TherapeuticClassID, s.repo.GetTherapeuticClassForUpdate, resTherapeuticClass); err != nil {
			return err
		}
		saved, err := s.repo.UpdateGenericMedicine(ctx, current)
		if err != nil {
			return mapWriteErr(err, resGenericMedicine)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGenericMedicine soft-deletes a generic medicine, refusing while a live
// medicine still names it (D-19).
//
// This is the end of the taxonomy chain, so it is the one place where a generic
// medicine is both a child (of a therapeutic class, checked by that class's probe)
// and a parent (of medicines.generic_medicine_id, checked here).
func (s *Service) DeleteGenericMedicine(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetGenericMedicineForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resGenericMedicine)
		}
		if err := checkUnreferenced(ctx, resGenericMedicine, id, s.repo.IsGenericMedicineReferenced); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteGenericMedicine(ctx, id)
		if err != nil {
			return mapWriteErr(err, resGenericMedicine)
		}
		if !ok {
			return errs.NotFound(resGenericMedicine)
		}
		return nil
	})
}

// applyGenericMedicinePatch copies the non-nil fields of the PATCH onto g.
func applyGenericMedicinePatch(g *GenericMedicine, req *UpdateGenericMedicineRequest) {
	if req.Name != nil {
		g.Name = *req.Name
	}
	if req.Description != nil {
		g.Description = req.Description
	}
	if req.TherapeuticClassID != nil {
		g.TherapeuticClassID = req.TherapeuticClassID
	}
	if req.IsControlled != nil {
		g.IsControlled = *req.IsControlled
	}
	if req.IsActive != nil {
		g.IsActive = *req.IsActive
	}
}
