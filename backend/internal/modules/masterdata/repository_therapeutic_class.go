package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// therapeuticClassColumns mirrors the declaration order of
// 000005_master_data.up.sql:193-206.
const therapeuticClassColumns = `id, code, name, drug_class_id, description, is_active, ` +
	`created_at, updated_at, deleted_at`

// therapeuticClassTable describes therapeutic_classes. parentCol is drug_class_id
// because ListMasterDataQuery contracts ?parent_id= to this hierarchy.
var therapeuticClassTable = tableSpec{
	table:        "therapeutic_classes",
	columns:      therapeuticClassColumns,
	insertCols:   `code, name, drug_class_id, description, is_active`,
	updateCols:   `name, drug_class_id, description, is_active`,
	parentCol:    "drug_class_id",
	sortColumns:  therapeuticClassSortColumns,
	defaultOrder: defaultOrder,
}

// scanTherapeuticClass maps one row into a TherapeuticClass.
func scanTherapeuticClass(row rowScanner) (*TherapeuticClass, error) {
	var t TherapeuticClass
	if err := row.Scan(
		&t.ID, &t.Code, &t.Name, &t.DrugClassID, &t.Description,
		&t.IsActive, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

// therapeuticClassValues binds t as [id, code, then every mutable column].
func therapeuticClassValues(t *TherapeuticClass) []any {
	return []any{t.ID, t.Code, t.Name, t.DrugClassID, t.Description, t.IsActive}
}

// CreateTherapeuticClass inserts a therapeutic class and returns the stored row.
func (r *Repository) CreateTherapeuticClass(ctx context.Context, t *TherapeuticClass) (*TherapeuticClass, error) {
	return createRow(ctx, r, therapeuticClassTable, t, therapeuticClassValues, scanTherapeuticClass)
}

// GetTherapeuticClass returns the live therapeutic class with the given id, or
// db.ErrNoRows.
func (r *Repository) GetTherapeuticClass(ctx context.Context, id uuid.UUID) (*TherapeuticClass, error) {
	return getRow(ctx, r, therapeuticClassTable, id, scanTherapeuticClass, false)
}

// GetTherapeuticClassForUpdate is GetTherapeuticClass with a row lock.
func (r *Repository) GetTherapeuticClassForUpdate(ctx context.Context, id uuid.UUID) (*TherapeuticClass, error) {
	return getRow(ctx, r, therapeuticClassTable, id, scanTherapeuticClass, true)
}

// ListTherapeuticClasses returns a filtered, paginated page plus the total count.
// ListFilter.ParentID filters on drug_class_id.
func (r *Repository) ListTherapeuticClasses(ctx context.Context, f ListFilter, o pagination.Offset) ([]TherapeuticClass, int64, error) {
	return listRows(ctx, r, therapeuticClassTable, scanTherapeuticClass, f, o)
}

// UpdateTherapeuticClass writes the mutable columns of t and returns the refreshed
// row. code and id are never written.
func (r *Repository) UpdateTherapeuticClass(ctx context.Context, t *TherapeuticClass) (*TherapeuticClass, error) {
	return updateRow(ctx, r, therapeuticClassTable, t, therapeuticClassValues, scanTherapeuticClass)
}

// SoftDeleteTherapeuticClass marks the therapeutic class deleted and reports whether a
// row was affected.
//
// therapeutic_classes is not referenced by medicines (contract C-11), so there is no
// Is...Referenced probe. It does have generic_medicines children whose foreign key is
// ON DELETE SET NULL, which this soft delete does not fire — and those children may
// in turn be referenced by medicines, so the service has to check the whole subtree
// itself.
func (r *Repository) SoftDeleteTherapeuticClass(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, therapeuticClassTable, id)
}

// HasTherapeuticClassChildren reports whether any live generic medicine still names
// this therapeutic class.
//
// Last link of the taxonomy chain, and the one the medicines probe cannot cover:
// a generic medicine below this class is exactly the row that would be detached,
// even though no medicine points at the therapeutic class directly. The service
// checks this before the class's own medicines-facing children go away.
func (r *Repository) HasTherapeuticClassChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByChild(ctx, r, "generic_medicines", "therapeutic_class_id", id)
}
