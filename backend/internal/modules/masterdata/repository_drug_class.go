package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// drugClassColumns mirrors the declaration order of 000005_master_data.up.sql:178-191.
const drugClassColumns = `id, code, name, drug_group_id, description, is_active, ` +
	`created_at, updated_at, deleted_at`

// drugClassTable describes drug_classes. parentCol is drug_group_id because
// ListMasterDataQuery contracts ?parent_id= to this hierarchy.
var drugClassTable = tableSpec{
	table:        "drug_classes",
	columns:      drugClassColumns,
	insertCols:   `code, name, drug_group_id, description, is_active`,
	updateCols:   `name, drug_group_id, description, is_active`,
	parentCol:    "drug_group_id",
	sortColumns:  drugClassSortColumns,
	defaultOrder: defaultOrder,
}

// scanDrugClass maps one row into a DrugClass.
func scanDrugClass(row rowScanner) (*DrugClass, error) {
	var c DrugClass
	if err := row.Scan(
		&c.ID, &c.Code, &c.Name, &c.DrugGroupID, &c.Description,
		&c.IsActive, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

// drugClassValues binds c as [id, code, then every mutable column].
func drugClassValues(c *DrugClass) []any {
	return []any{c.ID, c.Code, c.Name, c.DrugGroupID, c.Description, c.IsActive}
}

// CreateDrugClass inserts a drug class and returns the stored row. The service
// resolves c.DrugGroupID to a live group first; a stale one surfaces here as a
// foreign-key violation.
func (r *Repository) CreateDrugClass(ctx context.Context, c *DrugClass) (*DrugClass, error) {
	return createRow(ctx, r, drugClassTable, c, drugClassValues, scanDrugClass)
}

// GetDrugClass returns the live drug class with the given id, or db.ErrNoRows.
func (r *Repository) GetDrugClass(ctx context.Context, id uuid.UUID) (*DrugClass, error) {
	return getRow(ctx, r, drugClassTable, id, scanDrugClass, false)
}

// GetDrugClassForUpdate is GetDrugClass with a row lock.
func (r *Repository) GetDrugClassForUpdate(ctx context.Context, id uuid.UUID) (*DrugClass, error) {
	return getRow(ctx, r, drugClassTable, id, scanDrugClass, true)
}

// ListDrugClasses returns a filtered, paginated page plus the total count.
// ListFilter.ParentID filters on drug_group_id.
func (r *Repository) ListDrugClasses(ctx context.Context, f ListFilter, o pagination.Offset) ([]DrugClass, int64, error) {
	return listRows(ctx, r, drugClassTable, scanDrugClass, f, o)
}

// UpdateDrugClass writes the mutable columns of c and returns the refreshed row. code
// and id are never written.
func (r *Repository) UpdateDrugClass(ctx context.Context, c *DrugClass) (*DrugClass, error) {
	return updateRow(ctx, r, drugClassTable, c, drugClassValues, scanDrugClass)
}

// SoftDeleteDrugClass marks the drug class deleted and reports whether a row was
// affected.
//
// drug_classes is not referenced by medicines (contract C-11), so there is no
// Is...Referenced probe. It does have therapeutic_classes children whose foreign key
// is ON DELETE SET NULL, which this soft delete does not fire — the service checks
// for children itself.
func (r *Repository) SoftDeleteDrugClass(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, drugClassTable, id)
}

// HasDrugClassChildren reports whether any live therapeutic class still names this
// class. Second link of the taxonomy chain; see HasDrugGroupChildren.
func (r *Repository) HasDrugClassChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByChild(ctx, r, "therapeutic_classes", "drug_class_id", id)
}
