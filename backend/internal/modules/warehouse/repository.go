package warehouse

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository is the warehouse data-access layer. Every method is tenant-scoped
// by organization id; branch-scoped queries additionally filter branch_id.
// Methods return raw driver errors for the service to translate.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// warehouseColumns is the canonical column order shared by every SELECT and the
// INSERT/UPDATE ... RETURNING. scanWarehouse Scans in exactly this order.
const warehouseColumns = `id, organization_id, branch_id, code, name, location, ` +
	`capacity, is_active, is_main, created_at, updated_at, deleted_at`

// scanWarehouse maps one row into a Warehouse.
func scanWarehouse(row pgx.Row) (*Warehouse, error) {
	var w Warehouse
	if err := row.Scan(
		&w.ID, &w.OrganizationID, &w.BranchID, &w.Code, &w.Name, &w.Location,
		&w.Capacity, &w.IsActive, &w.IsMain, &w.CreatedAt, &w.UpdatedAt, &w.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &w, nil
}

// BranchBelongsToOrg reports whether branchID is a live (non-deleted) branch of
// orgID. The service calls it before insert because branch_id comes from client
// input: without this check a caller could attach a warehouse to another
// tenant's branch. Uses the primary within a tx so the check and the subsequent
// insert see a consistent view.
func (r *Repository) BranchBelongsToOrg(ctx context.Context, orgID, branchID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (
			SELECT 1 FROM branches
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		)`
	var ok bool
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, branchID, orgID).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// Insert creates a warehouse and returns the stored row. organization_id and
// branch_id must already be set on w by the service. A duplicate
// (branch_id, code) surfaces as a unique violation.
func (r *Repository) Insert(ctx context.Context, w *Warehouse) (*Warehouse, error) {
	const q = `INSERT INTO warehouses (
			organization_id, branch_id, code, name, location, capacity, is_active, is_main
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING ` + warehouseColumns
	return scanWarehouse(r.db.FromCtx(ctx).QueryRow(ctx, q,
		w.OrganizationID, w.BranchID, w.Code, w.Name, w.Location, w.Capacity, w.IsActive, w.IsMain,
	))
}

// FindByID returns the non-deleted warehouse with the given id within orgID.
// The org filter makes cross-tenant reads indistinguishable from missing.
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*Warehouse, error) {
	const q = `SELECT ` + warehouseColumns + `
		FROM warehouses
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanWarehouse(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByIDForUpdate is FindByID with a row lock, for the read-modify-write of a
// PATCH/DELETE inside a transaction. Always targets the primary.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Warehouse, error) {
	const q = `SELECT ` + warehouseColumns + `
		FROM warehouses
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`
	return scanWarehouse(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// Update writes the mutable columns of w and returns the refreshed row. code,
// organization_id and branch_id are never changed here. Returns db.ErrNoRows if
// the row is missing/deleted.
func (r *Repository) Update(ctx context.Context, w *Warehouse) (*Warehouse, error) {
	const q = `UPDATE warehouses SET
			name = $3, location = $4, capacity = $5, is_active = $6, is_main = $7
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + warehouseColumns
	return scanWarehouse(r.db.FromCtx(ctx).QueryRow(ctx, q,
		w.ID, w.OrganizationID, w.Name, w.Location, w.Capacity, w.IsActive, w.IsMain,
	))
}

// SoftDelete marks the warehouse deleted within orgID and reports whether a row
// was affected. The partial unique index on (branch_id, code) excludes deleted
// rows, so the code becomes reusable immediately.
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `UPDATE warehouses SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ClearMainExcept unsets is_main on every non-deleted warehouse of branchID
// other than exceptID, preserving the "at most one main warehouse per branch"
// invariant. Pass uuid.Nil as exceptID to clear the flag on all of them.
func (r *Repository) ClearMainExcept(ctx context.Context, branchID, exceptID uuid.UUID) error {
	const q = `UPDATE warehouses SET is_main = FALSE
		WHERE branch_id = $1 AND id <> $2 AND is_main = TRUE AND deleted_at IS NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, branchID, exceptID)
	return err
}

// listSortColumns is the allow-list of columns the list endpoints may sort by.
var listSortColumns = map[string]struct{}{
	"code":       {},
	"name":       {},
	"created_at": {},
	"is_active":  {},
}

// ListFilter carries the optional filters for List (decoupled from the HTTP DTO).
// BranchID nil = across all branches in the org; non-nil = that branch only.
// BranchScope, when non-nil, is the caller's effective branch subset (ADR §19):
// the query is then constrained to warehouses whose branch is in that set, so a
// branch-bound principal can never observe a warehouse in an unassigned branch
// even if a client supplies another branch_id (§84 "scoped repository query").
// A nil BranchScope means the caller is org-wide and no row-level branch
// restriction is applied.
type ListFilter struct {
	BranchID    *uuid.UUID
	BranchScope []uuid.UUID
	IsActive    *bool
	IsMain      *bool
}

// List returns a filtered, paginated page of warehouses for orgID plus the total
// count matching the same filters. All values are passed as bind parameters.
func (r *Repository) List(ctx context.Context, orgID uuid.UUID, f ListFilter, o pagination.Offset) ([]ListItem, int64, error) {
	where := []string{"organization_id = $1", "deleted_at IS NULL"}
	args := []any{orgID}

	if len(f.BranchScope) > 0 {
		args = append(args, f.BranchScope)
		where = append(where, "branch_id = ANY($"+strconv.Itoa(len(args))+")")
	} else if f.BranchID != nil {
		args = append(args, *f.BranchID)
		where = append(where, "branch_id = $"+strconv.Itoa(len(args)))
	}
	if f.IsActive != nil {
		args = append(args, *f.IsActive)
		where = append(where, "is_active = $"+strconv.Itoa(len(args)))
	}
	if f.IsMain != nil {
		args = append(args, *f.IsMain)
		where = append(where, "is_main = $"+strconv.Itoa(len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	countSQL := `SELECT count(*) FROM warehouses WHERE ` + whereSQL
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []ListItem{}, 0, nil
	}

	orderSQL := buildOrderBy(o.Sort)
	args = append(args, o.Limit, o.OffsetSQL())
	listSQL := `SELECT id, branch_id, code, name, is_active, is_main, capacity
		FROM warehouses
		WHERE ` + whereSQL + `
		` + orderSQL + `
		LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]ListItem, 0, o.Limit)
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(&it.ID, &it.BranchID, &it.Code, &it.Name, &it.IsActive, &it.IsMain, &it.Capacity); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// buildOrderBy turns a validated "sort=" string into an ORDER BY clause using
// only allow-listed columns, with a deterministic default and an id tie-breaker.
func buildOrderBy(sort string) string {
	fields, err := pagination.ParseSort(sort, listSortColumns)
	if err != nil || len(fields) == 0 {
		return "ORDER BY code ASC, id ASC"
	}
	parts := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		dir := "ASC"
		if f.Desc {
			dir = "DESC"
		}
		parts = append(parts, f.Field+" "+dir)
	}
	parts = append(parts, "id ASC")
	return "ORDER BY " + strings.Join(parts, ", ")
}
