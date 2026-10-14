package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateDrugClass inserts a drug class.
//
// drug_group_id is optional. When supplied the operation is two statements — prove
// the group is live, then insert — so it runs in a transaction (contract §15).
// IsActive is resolved so an omitted value stores the column default TRUE (§28.5).
func (s *Service) CreateDrugClass(ctx context.Context, req *CreateDrugClassRequest) (*DrugClass, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	c := &DrugClass{
		Code:        req.Code,
		Name:        req.Name,
		DrugGroupID: req.DrugGroupID,
		Description: req.Description,
		IsActive:    boolOr(req.IsActive, true),
	}
	if req.DrugGroupID == nil {
		return s.insertDrugClass(ctx, c)
	}

	var out *DrugClass
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := requireReference(ctx, req.DrugGroupID, s.repo.GetDrugGroupForUpdate, resDrugGroup); err != nil {
			return err
		}
		created, err := s.repo.CreateDrugClass(ctx, c)
		if err != nil {
			return mapWriteErr(err, resDrugClass)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// insertDrugClass is the single-statement create path for an ungrouped class.
func (s *Service) insertDrugClass(ctx context.Context, c *DrugClass) (*DrugClass, error) {
	created, err := s.repo.CreateDrugClass(ctx, c)
	if err != nil {
		return nil, mapWriteErr(err, resDrugClass)
	}
	return created, nil
}

// GetDrugClass returns one live drug class, or NotFound.
func (s *Service) GetDrugClass(ctx context.Context, id uuid.UUID) (*DrugClass, error) {
	c, err := s.repo.GetDrugClass(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resDrugClass)
	}
	return c, nil
}

// ListDrugClasses returns a filtered, paginated page of drug classes plus meta. This
// is one of the three resources whose ?parent_id= filter is honoured, filtering on
// drug_group_id.
func (s *Service) ListDrugClasses(ctx context.Context, q *ListMasterDataQuery) ([]DrugClass, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListDrugClasses)
}

// UpdateDrugClass applies a partial change, including attaching or detaching a drug
// group, under a row lock in one transaction. An empty PATCH degrades to a read;
// code is immutable and not patchable.
func (s *Service) UpdateDrugClass(ctx context.Context, id uuid.UUID, req *UpdateDrugClassRequest) (*DrugClass, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetDrugClass(ctx, id)
	}

	var out *DrugClass
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetDrugClassForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resDrugClass)
		}
		applyDrugClassPatch(current, req)
		if err := requireReference(ctx, current.DrugGroupID, s.repo.GetDrugGroupForUpdate, resDrugGroup); err != nil {
			return err
		}
		saved, err := s.repo.UpdateDrugClass(ctx, current)
		if err != nil {
			return mapWriteErr(err, resDrugClass)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteDrugClass soft-deletes a drug class.
//
// There is no medicines probe for this resource (C-11), so HasDrugClassChildren is
// the only guard: therapeutic_classes.drug_class_id is SET NULL, which a soft delete
// does not fire, so deleting a class mid-taxonomy would detach every therapeutic
// class beneath it — and the generic medicines beneath those.
func (s *Service) DeleteDrugClass(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetDrugClassForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resDrugClass)
		}
		if err := checkUnreferenced(ctx, resDrugClass, id, s.repo.HasDrugClassChildren); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteDrugClass(ctx, id)
		if err != nil {
			return mapWriteErr(err, resDrugClass)
		}
		if !ok {
			return errs.NotFound(resDrugClass)
		}
		return nil
	})
}

// applyDrugClassPatch copies the non-nil fields of the PATCH onto c.
func applyDrugClassPatch(c *DrugClass, req *UpdateDrugClassRequest) {
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.DrugGroupID != nil {
		c.DrugGroupID = req.DrugGroupID
	}
	if req.Description != nil {
		c.Description = req.Description
	}
	if req.IsActive != nil {
		c.IsActive = *req.IsActive
	}
}
