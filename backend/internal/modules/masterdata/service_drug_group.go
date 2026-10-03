package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateDrugGroup inserts a drug group.
//
// DrugGroup is the root of the taxonomy chain (contract §20 note 2): nothing
// references it except its own drug_classes children, so this is a single statement
// with no transaction. IsActive is resolved so an omitted value stores the column
// default TRUE (§28.5).
func (s *Service) CreateDrugGroup(ctx context.Context, req *CreateDrugGroupRequest) (*DrugGroup, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	g := &DrugGroup{
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		IsActive:    boolOr(req.IsActive, true),
	}
	created, err := s.repo.CreateDrugGroup(ctx, g)
	if err != nil {
		return nil, mapWriteErr(err, resDrugGroup)
	}
	return created, nil
}

// GetDrugGroup returns one live drug group, or NotFound.
func (s *Service) GetDrugGroup(ctx context.Context, id uuid.UUID) (*DrugGroup, error) {
	g, err := s.repo.GetDrugGroup(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resDrugGroup)
	}
	return g, nil
}

// ListDrugGroups returns a filtered, paginated page of drug groups plus meta.
func (s *Service) ListDrugGroups(ctx context.Context, q *ListMasterDataQuery) ([]DrugGroup, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListDrugGroups)
}

// UpdateDrugGroup applies a partial change under a row lock. An empty PATCH
// degrades to a read; code is immutable and not patchable.
func (s *Service) UpdateDrugGroup(ctx context.Context, id uuid.UUID, req *UpdateDrugGroupRequest) (*DrugGroup, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetDrugGroup(ctx, id)
	}

	var out *DrugGroup
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetDrugGroupForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resDrugGroup)
		}
		applyDrugGroupPatch(current, req)
		saved, err := s.repo.UpdateDrugGroup(ctx, current)
		if err != nil {
			return mapWriteErr(err, resDrugGroup)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteDrugGroup soft-deletes a drug group.
//
// There is no Is...Referenced probe for this resource — no downstream table points
// at drug_groups (contract C-11) — so HasDrugGroupChildren is the ONLY thing
// standing between a DELETE and an orphaned taxonomy. drug_classes.drug_group_id is
// SET NULL, and a soft delete does not fire that key, so without the probe every
// drug class beneath this group would be silently detached from its tree.
func (s *Service) DeleteDrugGroup(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetDrugGroupForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resDrugGroup)
		}
		if err := checkUnreferenced(ctx, resDrugGroup, id, s.repo.HasDrugGroupChildren); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteDrugGroup(ctx, id)
		if err != nil {
			return mapWriteErr(err, resDrugGroup)
		}
		if !ok {
			return errs.NotFound(resDrugGroup)
		}
		return nil
	})
}

// applyDrugGroupPatch copies the non-nil fields of the PATCH onto g.
func applyDrugGroupPatch(g *DrugGroup, req *UpdateDrugGroupRequest) {
	if req.Name != nil {
		g.Name = *req.Name
	}
	if req.Description != nil {
		g.Description = req.Description
	}
	if req.IsActive != nil {
		g.IsActive = *req.IsActive
	}
}
