package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreatePackageType inserts a package type.
//
// Single statement, no foreign key, no cross-field rule — no transaction, per
// contract §15. IsActive is resolved so an omitted value stores the column default
// TRUE rather than false (§28.5).
func (s *Service) CreatePackageType(ctx context.Context, req *CreatePackageTypeRequest) (*PackageType, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	p := &PackageType{
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		IsActive:    boolOr(req.IsActive, true),
	}
	created, err := s.repo.CreatePackageType(ctx, p)
	if err != nil {
		return nil, mapWriteErr(err, resPackageType)
	}
	return created, nil
}

// GetPackageType returns one live package type, or NotFound.
func (s *Service) GetPackageType(ctx context.Context, id uuid.UUID) (*PackageType, error) {
	p, err := s.repo.GetPackageType(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resPackageType)
	}
	return p, nil
}

// ListPackageTypes returns a filtered, paginated page of package types plus meta.
func (s *Service) ListPackageTypes(ctx context.Context, q *ListMasterDataQuery) ([]PackageType, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListPackageTypes)
}

// UpdatePackageType applies a partial change under a row lock. An empty PATCH
// degrades to a read; code is immutable and not patchable.
func (s *Service) UpdatePackageType(ctx context.Context, id uuid.UUID, req *UpdatePackageTypeRequest) (*PackageType, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetPackageType(ctx, id)
	}

	var out *PackageType
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetPackageTypeForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resPackageType)
		}
		applyPackageTypePatch(current, req)
		saved, err := s.repo.UpdatePackageType(ctx, current)
		if err != nil {
			return mapWriteErr(err, resPackageType)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeletePackageType soft-deletes a package type, refusing while a live medicine
// still names it (D-19). medicines.package_type_id is SET NULL, which a soft delete
// does not fire, so the probe is the only thing standing between a DELETE and a
// silently detached medicine.
func (s *Service) DeletePackageType(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetPackageTypeForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resPackageType)
		}
		if err := checkUnreferenced(ctx, resPackageType, id, s.repo.IsPackageTypeReferenced); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeletePackageType(ctx, id)
		if err != nil {
			return mapWriteErr(err, resPackageType)
		}
		if !ok {
			return errs.NotFound(resPackageType)
		}
		return nil
	})
}

// applyPackageTypePatch copies the non-nil fields of the PATCH onto p.
func applyPackageTypePatch(p *PackageType, req *UpdatePackageTypeRequest) {
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Description != nil {
		p.Description = req.Description
	}
	if req.IsActive != nil {
		p.IsActive = *req.IsActive
	}
}
