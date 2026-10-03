package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// routeColumns is the canonical column order shared by every SELECT and by every
// INSERT/UPDATE ... RETURNING. Mirrors the declaration order of
// 000005_master_data.up.sql:19-28.
const routeColumns = `id, code, name, description, is_active, created_at, updated_at, deleted_at`

// routeTable describes routes for the shared query core.
//
// routes has no relationship to another MasterData resource, so parentCol is empty
// and any ListFilter parent fields are ignored. The SQL itself is not written here:
// the core builds it from insertCols/updateCols, so the only thing this file declares
// is what is specific to routes.
var routeTable = tableSpec{
	table:        "routes",
	columns:      routeColumns,
	insertCols:   `code, name, description, is_active`,
	updateCols:   `name, description, is_active`,
	sortColumns:  routeSortColumns,
	defaultOrder: defaultOrder,
}

// scanRoute maps one row into a Route.
func scanRoute(row rowScanner) (*Route, error) {
	var v Route
	if err := row.Scan(
		&v.ID, &v.Code, &v.Name, &v.Description,
		&v.IsActive, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// routeValues binds v as [id, code, then every mutable column]. The core derives the
// INSERT arguments (everything after the id) and the UPDATE arguments (id, then
// everything after code) from this ordering.
func routeValues(v *Route) []any {
	return []any{v.ID, v.Code, v.Name, v.Description, v.IsActive}
}

// CreateRoute inserts a route and returns the stored row. A duplicate code surfaces
// as a unique violation from ux_routes_code.
func (r *Repository) CreateRoute(ctx context.Context, v *Route) (*Route, error) {
	return createRow(ctx, r, routeTable, v, routeValues, scanRoute)
}

// GetRoute returns the live route with the given id, or db.ErrNoRows.
func (r *Repository) GetRoute(ctx context.Context, id uuid.UUID) (*Route, error) {
	return getRow(ctx, r, routeTable, id, scanRoute, false)
}

// GetRouteForUpdate is GetRoute with a row lock, for the read-modify-write of a
// PATCH/DELETE inside a transaction.
func (r *Repository) GetRouteForUpdate(ctx context.Context, id uuid.UUID) (*Route, error) {
	return getRow(ctx, r, routeTable, id, scanRoute, true)
}

// ListRoutes returns a filtered, paginated page plus the total count.
func (r *Repository) ListRoutes(ctx context.Context, f ListFilter, o pagination.Offset) ([]Route, int64, error) {
	return listRows(ctx, r, routeTable, scanRoute, f, o)
}

// UpdateRoute writes the mutable columns of v and returns the refreshed row. code
// and id are never written.
func (r *Repository) UpdateRoute(ctx context.Context, v *Route) (*Route, error) {
	return updateRow(ctx, r, routeTable, v, routeValues, scanRoute)
}

// SoftDeleteRoute marks the route deleted and reports whether a row was affected.
func (r *Repository) SoftDeleteRoute(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, routeTable, id)
}

// IsRouteReferenced reports whether any live medicine still points at this route via
// medicines.route_id (000006:13). See IsProductCategoryReferenced for why the foreign
// key alone is not enough.
func (r *Repository) IsRouteReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "route_id", id)
}
