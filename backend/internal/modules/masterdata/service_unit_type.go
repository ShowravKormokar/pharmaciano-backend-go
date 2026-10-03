package masterdata

import (
	"context"

	"github.com/google/uuid"

	errs "backend/internal/errors"
	"backend/pkg/pagination"
)

// CreateUnitType inserts a unit type.
//
// A unit is either a base unit (base_unit_id nil) or derived from one, so the
// base_unit_id path is a two-statement operation and runs in a transaction.
//
// ConversionFactor is nullable with no schema default, so nil stays nil and the
// column is left empty — that is correct here and is the one numeric column that
// needs no resolution. It is still checked for representability, so a NaN or an
// out-of-precision factor is rejected here rather than reaching PostgreSQL.
//
// IsActive is resolved so an omitted value stores the column default TRUE rather
// than false (§28.5).
func (s *Service) CreateUnitType(ctx context.Context, req *CreateUnitTypeRequest) (*UnitType, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if err := requireConversionFactor(req.ConversionFactor); err != nil {
		return nil, err
	}
	u := &UnitType{
		Code:             req.Code,
		Name:             req.Name,
		Symbol:           req.Symbol,
		BaseUnitID:       req.BaseUnitID,
		ConversionFactor: req.ConversionFactor,
		IsActive:         boolOr(req.IsActive, true),
	}
	if req.BaseUnitID == nil {
		return s.insertUnitType(ctx, u)
	}

	var out *UnitType
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		if err := requireReference(ctx, req.BaseUnitID, s.repo.GetUnitType, resUnitType); err != nil {
			return err
		}
		created, err := s.repo.CreateUnitType(ctx, u)
		if err != nil {
			return mapWriteErr(err, resUnitType)
		}
		out = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// insertUnitType is the single-statement create path for a base unit.
func (s *Service) insertUnitType(ctx context.Context, u *UnitType) (*UnitType, error) {
	created, err := s.repo.CreateUnitType(ctx, u)
	if err != nil {
		return nil, mapWriteErr(err, resUnitType)
	}
	return created, nil
}

// GetUnitType returns one live unit type, or NotFound.
func (s *Service) GetUnitType(ctx context.Context, id uuid.UUID) (*UnitType, error) {
	u, err := s.repo.GetUnitType(ctx, id)
	if err != nil {
		return nil, mapReadErr(err, resUnitType)
	}
	return u, nil
}

// ListUnitTypes returns a filtered, paginated page of unit types plus meta.
//
// unit_types.base_unit_id is indexed but not filterable: the API contract defines
// no parameter for it (contract §28.4).
func (s *Service) ListUnitTypes(ctx context.Context, q *ListMasterDataQuery) ([]UnitType, pagination.Meta, error) {
	return listPage(ctx, q, s.repo.ListUnitTypes)
}

// UpdateUnitType applies a partial change under a row lock in one transaction.
//
// base_unit_id may not be patched to this row's own id; that is rejected as a
// business-rule violation and is independently enforced by
// ck_unit_types_base_unit_not_self (000020).
//
// Multi-node cycles (A -> B -> A) are NOT detected here. Contract §11.2 and §23
// defer that to Phase 06 with an explicit decision, because no traversal or depth
// policy is defined anywhere in the repository; this phase refuses only the direct
// self-reference it can decide from the payload.
func (s *Service) UpdateUnitType(ctx context.Context, id uuid.UUID, req *UpdateUnitTypeRequest) (*UnitType, error) {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if req.IsEmpty() {
		return s.GetUnitType(ctx, id)
	}
	if err := requireConversionFactor(req.ConversionFactor); err != nil {
		return nil, err
	}

	var out *UnitType
	err := s.db.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.repo.GetUnitTypeForUpdate(ctx, id)
		if err != nil {
			return mapReadErr(err, resUnitType)
		}
		applyUnitTypePatch(current, req)
		if err := ValidateSelfReference("base_unit_id", current.ID, current.BaseUnitID); err != nil {
			return ruleViolation(err)
		}
		if err := requireReference(ctx, current.BaseUnitID, s.repo.GetUnitType, resUnitType); err != nil {
			return err
		}
		saved, err := s.repo.UpdateUnitType(ctx, current)
		if err != nil {
			return mapWriteErr(err, resUnitType)
		}
		out = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteUnitType soft-deletes a unit type.
//
// Both probes are required: a unit can be in use as the unit a medicine is SOLD in
// (medicines.sales_unit_id), or as the BASE of a larger unit. The second is
// invisible to the medicines probe, so removing a base unit would otherwise detach
// every unit derived from it.
func (s *Service) DeleteUnitType(ctx context.Context, id uuid.UUID) error {
	if err := s.requireSuperAdmin(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetUnitTypeForUpdate(ctx, id); err != nil {
			return mapReadErr(err, resUnitType)
		}
		err := checkUnreferenced(ctx, resUnitType, id,
			s.repo.IsUnitTypeReferenced,
			s.repo.HasUnitTypeChildren,
		)
		if err != nil {
			return err
		}
		ok, err := s.repo.SoftDeleteUnitType(ctx, id)
		if err != nil {
			return mapWriteErr(err, resUnitType)
		}
		if !ok {
			return errs.NotFound(resUnitType)
		}
		return nil
	})
}

// applyUnitTypePatch copies the non-nil fields of the PATCH onto u.
func applyUnitTypePatch(u *UnitType, req *UpdateUnitTypeRequest) {
	if req.Name != nil {
		u.Name = *req.Name
	}
	if req.Symbol != nil {
		u.Symbol = req.Symbol
	}
	if req.BaseUnitID != nil {
		u.BaseUnitID = req.BaseUnitID
	}
	if req.ConversionFactor != nil {
		u.ConversionFactor = req.ConversionFactor
	}
	if req.IsActive != nil {
		u.IsActive = *req.IsActive
	}
}
