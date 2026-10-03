package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateStorageCondition inserts a storage condition.
//
// Both temperature bounds are optional and either may be omitted, so a condition can
// be open-ended ("store below 25 degrees"). The band is still checked here as a pair
// on create: the DTO registers the same rule at bind time, and re-checking costs
// nothing and means a caller that skipped the handler cannot write an inverted band.
//
// Single statement, no foreign key, so no transaction (contract §15). IsActive is
// resolved so an omitted value stores the column default TRUE (§28.5).
func (s *Service) CreateStorageCondition(ctx context.Context, req *CreateStorageConditionRequest) (*StorageCondition, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if err := requireTemperatureRange(req.MinTempCelsius, req.MaxTempCelsius); err != nil {
		return nil, err
	}
	sc := &StorageCondition{
		Code:           req.Code,
		Name:           req.Name,
		MinTempCelsius: req.MinTempCelsius,
		MaxTempCelsius: req.MaxTempCelsius,
		Description:    req.Description,
		IsActive:       boolOr(req.IsActive, true),
	}
	created, err := s.repo.CreateStorageCondition(ctx, sc)
	if err != nil {
		return nil, mapWriteErr(err, resStorageCondition)
	}
	return created, nil
}

// GetStorageCondition returns one live storage condition, or NotFound.
func (s *Service) GetStorageCondition(ctx context.Context, id uuid.UUID) (*StorageCondition, error) {
	sc, err := s.repo.GetStorageCondition(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resStorageCondition)
	}
	return sc, nil
}

// ListStorageConditions returns a filtered, paginated page of storage conditions
// plus meta.
func (s *Service) ListStorageConditions(ctx context.Context, q *ListMasterDataQuery) ([]StorageCondition, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListStorageConditions)
}

// UpdateStorageCondition applies a partial change under a row lock in one transaction.
//
// This is the case the DTO cannot decide. A PATCH that names only min_temp_celsius
// says nothing about whether the RESULT is ordered — that depends on the stored
// max_temp_celsius, which the request never mentions. So the patch is merged into
// the row read under FOR UPDATE and the MERGED pair is validated, which is exactly
// what ck_storage_conditions_temp_range will check a moment later. An empty PATCH
// degrades to a read; code is immutable and not patchable.
func (s *Service) UpdateStorageCondition(ctx context.Context, id uuid.UUID, req *UpdateStorageConditionRequest) (*StorageCondition, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetStorageCondition(ctx, id)
	}

	var out *StorageCondition
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetStorageConditionForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resStorageCondition)
		}
		applyStorageConditionPatch(current, req)
		if err := requireTemperatureRange(current.MinTempCelsius, current.MaxTempCelsius); err != nil {
			return err
		}
		saved, err := s.repo.UpdateStorageCondition(ctx, current)
		if err != nil {
			return mapWriteErr(err, resStorageCondition)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteStorageCondition soft-deletes a storage condition, refusing while a live
// medicine still names it (D-19).
func (s *Service) DeleteStorageCondition(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetStorageConditionForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resStorageCondition)
		}
		if err := checkUnreferenced(ctx, resStorageCondition, id, s.repo.IsStorageConditionReferenced); err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteStorageCondition(ctx, id)
		if err != nil {
			return mapWriteErr(err, resStorageCondition)
		}
		if !ok {
			return errs.NotFound(resStorageCondition)
		}
		return nil
	})
}

// applyStorageConditionPatch copies the non-nil fields of the PATCH onto s.
//
// MinTempCelsius and MaxTempCelsius are pointer columns that are genuinely
// nullable, but the PATCH type cannot express "set this to SQL NULL" — a non-nil
// pointer always carries a value. Omitting a bound therefore leaves it alone; there
// is currently no way to widen an existing band back to unbounded, which is recorded
// as a DTO limitation rather than worked around here.
func applyStorageConditionPatch(sc *StorageCondition, req *UpdateStorageConditionRequest) {
	if req.Name != nil {
		sc.Name = *req.Name
	}
	if req.MinTempCelsius != nil {
		sc.MinTempCelsius = req.MinTempCelsius
	}
	if req.MaxTempCelsius != nil {
		sc.MaxTempCelsius = req.MaxTempCelsius
	}
	if req.Description != nil {
		sc.Description = req.Description
	}
	if req.IsActive != nil {
		sc.IsActive = *req.IsActive
	}
}
