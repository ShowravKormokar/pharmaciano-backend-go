package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// productCategoryColumns mirrors the declaration order of
// 000005_master_data.up.sql:2-13.
const productCategoryColumns = `id, code, name, parent_id, description, sort_order, ` +
	`is_active, created_at, updated_at, deleted_at`

// productCategoryTable describes product_categories.
//
// parentCol is set because product_categories is one of the three self-referencing
// hierarchies ?parent_id= is contracted to filter. sort_order is sortable but is not
// the default order (see defaultOrder).
var productCategoryTable = tableSpec{
	table:        "product_categories",
	columns:      productCategoryColumns,
	insertCols:   `code, name, parent_id, description, sort_order, is_active`,
	updateCols:   `name, parent_id, description, sort_order, is_active`,
	parentCol:    "parent_id",
	sortColumns:  productCategorySortColumns,
	defaultOrder: defaultOrder,
}

// scanProductCategory maps one row into a ProductCategory.
func scanProductCategory(row rowScanner) (*ProductCategory, error) {
	var c ProductCategory
	if err := row.Scan(
		&c.ID, &c.Code, &c.Name, &c.ParentID, &c.Description, &c.SortOrder,
		&c.IsActive, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

// productCategoryValues binds c as [id, code, then every mutable column].
func productCategoryValues(c *ProductCategory) []any {
	return []any{c.ID, c.Code, c.Name, c.ParentID, c.Description, c.SortOrder, c.IsActive}
}

// CreateProductCategory inserts a category and returns the stored row.
//
// The service is responsible for having resolved c to a complete model — including
// c.ParentID pointing at a live parent — because the repository deliberately does no
// business validation (contract §12). The id, created_at, updated_at and deleted_at
// columns are left to their schema defaults.
//
// A duplicate code surfaces as a unique violation from ux_product_categories_code,
// and c.ParentID == c.ID as a check violation from
// ck_product_categories_parent_not_self (000020).
func (r *Repository) CreateProductCategory(ctx context.Context, c *ProductCategory) (*ProductCategory, error) {
	return createRow(ctx, r, productCategoryTable, c, productCategoryValues, scanProductCategory)
}

// GetProductCategory returns the live category with the given id, or db.ErrNoRows.
func (r *Repository) GetProductCategory(ctx context.Context, id uuid.UUID) (*ProductCategory, error) {
	return getRow(ctx, r, productCategoryTable, id, scanProductCategory, false)
}

// GetProductCategoryForUpdate is GetProductCategory with a row lock.
func (r *Repository) GetProductCategoryForUpdate(ctx context.Context, id uuid.UUID) (*ProductCategory, error) {
	return getRow(ctx, r, productCategoryTable, id, scanProductCategory, true)
}

// ListProductCategories returns a filtered, paginated page plus the total count.
// ListFilter.ParentID and ParentIsNull filter on parent_id; IsActive and Q apply as
// documented on ListFilter.
func (r *Repository) ListProductCategories(ctx context.Context, f ListFilter, o pagination.Offset) ([]ProductCategory, int64, error) {
	return listRows(ctx, r, productCategoryTable, scanProductCategory, f, o)
}

// UpdateProductCategory writes the mutable columns of c and returns the refreshed row.
// code and id are never written.
func (r *Repository) UpdateProductCategory(ctx context.Context, c *ProductCategory) (*ProductCategory, error) {
	return updateRow(ctx, r, productCategoryTable, c, productCategoryValues, scanProductCategory)
}

// SoftDeleteProductCategory marks the category deleted and reports whether a row was
// affected, so a second DELETE can answer 404 rather than silently succeeding.
func (r *Repository) SoftDeleteProductCategory(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, productCategoryTable, id)
}

// IsProductCategoryReferenced reports whether any live medicine still points at this
// category.
//
// The service calls it before SoftDeleteProductCategory and answers 409 when it is
// true. This is the D-19 guard, and it is necessary rather than belt-and-braces
// because the delete is soft: medicines.category_id is declared ON DELETE RESTRICT
// (000006:11), but a foreign key cannot see a row that is only marked deleted_at.
func (r *Repository) IsProductCategoryReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "category_id", id)
}

// HasProductCategoryChildren reports whether any live category still names this one
// as its parent.
//
// product_categories is self-referencing, so a category can be in use without any
// medicine pointing at it directly. Without this probe the service would remove a
// mid-tree category and silently promote its children to roots, because
// parent_id's ON DELETE SET NULL does not fire for a soft delete either.
func (r *Repository) HasProductCategoryChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByChild(ctx, r, "product_categories", "parent_id", id)
}
