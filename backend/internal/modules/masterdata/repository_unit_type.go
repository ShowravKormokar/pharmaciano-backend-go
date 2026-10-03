package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// unitTypeColumns mirrors the declaration order of 000005_master_data.up.sql:59-70.
const unitTypeColumns = `id, code, name, symbol, base_unit_id, conversion_factor, ` +
	`is_active, created_at, updated_at, deleted_at`

// unitTypeTable describes unit_types.
//
// base_unit_id is NOT the spec's parentCol: ?parent_id= is contracted to the three
// hierarchies and unit_types is not one of them, even though the column is
// self-referencing. The column is indexed by 000020 for the foreign key's benefit.
//
// NUMERIC: conversion_factor is NUMERIC(15,6) and scans into a *float64. pgx
// converts through its numeric codec, so a fractional value survives the round trip;
// note that float64 cannot represent every NUMERIC(15,6) exactly, so a value read
// back may differ from the value written in the last bits. That is accepted here
// because the model is float64 (Phase 02 decision) — a decimal type would be the
// fix if exact arithmetic is ever required.
var unitTypeTable = tableSpec{
	table:        "unit_types",
	columns:      unitTypeColumns,
	insertCols:   `code, name, symbol, base_unit_id, conversion_factor, is_active`,
	updateCols:   `name, symbol, base_unit_id, conversion_factor, is_active`,
	sortColumns:  unitTypeSortColumns,
	defaultOrder: defaultOrder,
}

// scanUnitType maps one row into a UnitType.
func scanUnitType(row rowScanner) (*UnitType, error) {
	var u UnitType
	if err := row.Scan(
		&u.ID, &u.Code, &u.Name, &u.Symbol, &u.BaseUnitID, &u.ConversionFactor,
		&u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &u, nil
}

// unitTypeValues binds u as [id, code, then every mutable column].
func unitTypeValues(u *UnitType) []any {
	return []any{u.ID, u.Code, u.Name, u.Symbol, u.BaseUnitID, u.ConversionFactor, u.IsActive}
}

// CreateUnitType inserts a unit and returns the stored row. A duplicate code
// surfaces as a unique violation from ux_unit_types_code; u.BaseUnitID == u.ID as a
// check violation from ck_unit_types_base_unit_not_self (000020).
//
// conversion_factor is written unconstrained: contract §11.2 keeps its bounds
// UNRESOLVED, so a zero or negative factor is stored rather than rejected.
func (r *Repository) CreateUnitType(ctx context.Context, u *UnitType) (*UnitType, error) {
	return createRow(ctx, r, unitTypeTable, u, unitTypeValues, scanUnitType)
}

// GetUnitType returns the live unit with the given id, or db.ErrNoRows.
func (r *Repository) GetUnitType(ctx context.Context, id uuid.UUID) (*UnitType, error) {
	return getRow(ctx, r, unitTypeTable, id, scanUnitType, false)
}

// GetUnitTypeForUpdate is GetUnitType with a row lock.
func (r *Repository) GetUnitTypeForUpdate(ctx context.Context, id uuid.UUID) (*UnitType, error) {
	return getRow(ctx, r, unitTypeTable, id, scanUnitType, true)
}

// ListUnitTypes returns a filtered, paginated page plus the total count.
func (r *Repository) ListUnitTypes(ctx context.Context, f ListFilter, o pagination.Offset) ([]UnitType, int64, error) {
	return listRows(ctx, r, unitTypeTable, scanUnitType, f, o)
}

// UpdateUnitType writes the mutable columns of u and returns the refreshed row. code
// and id are never written.
func (r *Repository) UpdateUnitType(ctx context.Context, u *UnitType) (*UnitType, error) {
	return updateRow(ctx, r, unitTypeTable, u, unitTypeValues, scanUnitType)
}

// SoftDeleteUnitType marks the unit deleted and reports whether a row was affected.
func (r *Repository) SoftDeleteUnitType(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, unitTypeTable, id)
}

// IsUnitTypeReferenced reports whether any live medicine still points at this unit
// via medicines.sales_unit_id (000006:21). That is the unit the medicine is sold in,
// so it is the reference that matters — not the unit another row names as its own
// base_unit_id, which is handled by the service's hierarchy rules.
func (r *Repository) IsUnitTypeReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "sales_unit_id", id)
}

// HasUnitTypeChildren reports whether any live unit still names this one as its
// base unit.
//
// IsUnitTypeReferenced only covers the sales reference. A unit can equally be in
// use as the base of a larger unit, and removing it would detach every child whose
// base_unit_id points at it.
func (r *Repository) HasUnitTypeChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByChild(ctx, r, "unit_types", "base_unit_id", id)
}
