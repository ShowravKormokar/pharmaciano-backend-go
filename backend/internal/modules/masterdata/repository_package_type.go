package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// packageTypeColumns mirrors the declaration order of 000005_master_data.up.sql:46-57.
const packageTypeColumns = `id, code, name, description, is_active, created_at, updated_at, deleted_at`

// packageTypeTable describes package_types. No relationship to another MasterData
// resource, so parentCol is empty.
var packageTypeTable = tableSpec{
	table:        "package_types",
	columns:      packageTypeColumns,
	insertCols:   `code, name, description, is_active`,
	updateCols:   `name, description, is_active`,
	sortColumns:  packageTypeSortColumns,
	defaultOrder: defaultOrder,
}

// scanPackageType maps one row into a PackageType.
func scanPackageType(row rowScanner) (*PackageType, error) {
	var p PackageType
	if err := row.Scan(
		&p.ID, &p.Code, &p.Name, &p.Description,
		&p.IsActive, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

// packageTypeValues binds p as [id, code, then every mutable column].
func packageTypeValues(p *PackageType) []any {
	return []any{p.ID, p.Code, p.Name, p.Description, p.IsActive}
}

// CreatePackageType inserts a package type and returns the stored row. A duplicate
// code surfaces as a unique violation from ux_package_types_code.
func (r *Repository) CreatePackageType(ctx context.Context, p *PackageType) (*PackageType, error) {
	return createRow(ctx, r, packageTypeTable, p, packageTypeValues, scanPackageType)
}

// GetPackageType returns the live package type with the given id, or db.ErrNoRows.
func (r *Repository) GetPackageType(ctx context.Context, id uuid.UUID) (*PackageType, error) {
	return getRow(ctx, r, packageTypeTable, id, scanPackageType, false)
}

// GetPackageTypeForUpdate is GetPackageType with a row lock.
func (r *Repository) GetPackageTypeForUpdate(ctx context.Context, id uuid.UUID) (*PackageType, error) {
	return getRow(ctx, r, packageTypeTable, id, scanPackageType, true)
}

// ListPackageTypes returns a filtered, paginated page plus the total count.
func (r *Repository) ListPackageTypes(ctx context.Context, f ListFilter, o pagination.Offset) ([]PackageType, int64, error) {
	return listRows(ctx, r, packageTypeTable, scanPackageType, f, o)
}

// UpdatePackageType writes the mutable columns of p and returns the refreshed row.
// code and id are never written.
func (r *Repository) UpdatePackageType(ctx context.Context, p *PackageType) (*PackageType, error) {
	return updateRow(ctx, r, packageTypeTable, p, packageTypeValues, scanPackageType)
}

// SoftDeletePackageType marks the package type deleted and reports whether a row was
// affected.
func (r *Repository) SoftDeletePackageType(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, packageTypeTable, id)
}

// IsPackageTypeReferenced reports whether any live medicine still points at this
// package type via medicines.package_type_id (000006:20).
func (r *Repository) IsPackageTypeReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "package_type_id", id)
}
