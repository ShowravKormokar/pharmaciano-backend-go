package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// storageConditionColumns mirrors the declaration order of
// 000005_master_data.up.sql:74-85.
const storageConditionColumns = `id, code, name, min_temp_celsius, max_temp_celsius, ` +
	`description, is_active, created_at, updated_at, deleted_at`

// storageConditionTable describes storage_conditions.
//
// NUMERIC: min_temp_celsius and max_temp_celsius are NUMERIC(5,2) and scan into
// *float64 — see the conversion_factor note on unitTypeTable about float64 precision.
var storageConditionTable = tableSpec{
	table:        "storage_conditions",
	columns:      storageConditionColumns,
	insertCols:   `code, name, min_temp_celsius, max_temp_celsius, description, is_active`,
	updateCols:   `name, min_temp_celsius, max_temp_celsius, description, is_active`,
	sortColumns:  storageConditionSortColumns,
	defaultOrder: defaultOrder,
}

// scanStorageCondition maps one row into a StorageCondition.
func scanStorageCondition(row rowScanner) (*StorageCondition, error) {
	var s StorageCondition
	if err := row.Scan(
		&s.ID, &s.Code, &s.Name, &s.MinTempCelsius, &s.MaxTempCelsius, &s.Description,
		&s.IsActive, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &s, nil
}

// storageConditionValues binds s as [id, code, then every mutable column].
func storageConditionValues(s *StorageCondition) []any {
	return []any{s.ID, s.Code, s.Name, s.MinTempCelsius, s.MaxTempCelsius, s.Description, s.IsActive}
}

// CreateStorageCondition inserts a storage condition and returns the stored row.
//
// An inverted band surfaces as a check violation from
// ck_storage_conditions_temp_range (000020) when both bounds are present; omitting
// either end is legal and means "unbounded on that side".
func (r *Repository) CreateStorageCondition(ctx context.Context, s *StorageCondition) (*StorageCondition, error) {
	return createRow(ctx, r, storageConditionTable, s, storageConditionValues, scanStorageCondition)
}

// GetStorageCondition returns the live storage condition with the given id, or
// db.ErrNoRows.
func (r *Repository) GetStorageCondition(ctx context.Context, id uuid.UUID) (*StorageCondition, error) {
	return getRow(ctx, r, storageConditionTable, id, scanStorageCondition, false)
}

// GetStorageConditionForUpdate is GetStorageCondition with a row lock.
func (r *Repository) GetStorageConditionForUpdate(ctx context.Context, id uuid.UUID) (*StorageCondition, error) {
	return getRow(ctx, r, storageConditionTable, id, scanStorageCondition, true)
}

// ListStorageConditions returns a filtered, paginated page plus the total count.
func (r *Repository) ListStorageConditions(ctx context.Context, f ListFilter, o pagination.Offset) ([]StorageCondition, int64, error) {
	return listRows(ctx, r, storageConditionTable, scanStorageCondition, f, o)
}

// UpdateStorageCondition writes the mutable columns of s and returns the refreshed
// row. code and id are never written.
func (r *Repository) UpdateStorageCondition(ctx context.Context, s *StorageCondition) (*StorageCondition, error) {
	return updateRow(ctx, r, storageConditionTable, s, storageConditionValues, scanStorageCondition)
}

// SoftDeleteStorageCondition marks the storage condition deleted and reports whether
// a row was affected.
func (r *Repository) SoftDeleteStorageCondition(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, storageConditionTable, id)
}

// IsStorageConditionReferenced reports whether any live medicine still points at
// this storage condition via medicines.storage_condition_id (000006:15).
func (r *Repository) IsStorageConditionReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "storage_condition_id", id)
}
