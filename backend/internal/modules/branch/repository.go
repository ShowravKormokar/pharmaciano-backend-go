package branch

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository is the branch data-access layer. Every method is tenant-scoped:
// the organization id is a mandatory argument on all reads and writes, so a
// query can never touch another tenant's rows. Methods return raw driver errors
// (db.ErrNoRows, unique-violation, …); the service maps them to domain errors.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// branchColumns is the canonical column order shared by every SELECT and the
// INSERT/UPDATE ... RETURNING. scanBranch Scans in exactly this order.
const branchColumns = `id, organization_id, code, name, is_active, is_default, ` +
	`address, city, state, postal_code, country, email, phone, ` +
	`latitude, longitude, open_time, close_time, created_at, updated_at, deleted_at`

// scanBranch maps one row into a Branch.
func scanBranch(row pgx.Row) (*Branch, error) {
	var b Branch
	if err := row.Scan(
		&b.ID, &b.OrganizationID, &b.Code, &b.Name, &b.IsActive, &b.IsDefault,
		&b.Address, &b.City, &b.State, &b.PostalCode, &b.Country, &b.Email, &b.Phone,
		&b.Latitude, &b.Longitude, &b.OpenTime, &b.CloseTime,
		&b.CreatedAt, &b.UpdatedAt, &b.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &b, nil
}

// Insert creates a branch and returns the stored row (server defaults and
// trigger-managed timestamps included). organization_id must already be set on
// b by the service from the caller's token. A duplicate (organization_id, code)
// surfaces as a unique violation for the service to translate to AlreadyExists.
func (r *Repository) Insert(ctx context.Context, b *Branch) (*Branch, error) {
	const q = `INSERT INTO branches (
			organization_id, code, name, is_active, is_default,
			address, city, state, postal_code, country, email, phone,
			latitude, longitude, open_time, close_time
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING ` + branchColumns
	return scanBranch(r.db.FromCtx(ctx).QueryRow(ctx, q,
		b.OrganizationID, b.Code, b.Name, b.IsActive, b.IsDefault,
		b.Address, b.City, b.State, b.PostalCode, b.Country, b.Email, b.Phone,
		b.Latitude, b.Longitude, b.OpenTime, b.CloseTime,
	))
}

// FindByID returns the non-deleted branch with the given id within orgID.
// Returns db.ErrNoRows if it does not exist, is soft-deleted, or belongs to a
// different organization (the org filter makes cross-tenant reads look missing).
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*Branch, error) {
	const q = `SELECT ` + branchColumns + `
		FROM branches
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanBranch(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByIDForUpdate is FindByID with a row lock, for the read-modify-write of a
// PATCH/PUT/DELETE inside a transaction. Always targets the primary.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Branch, error) {
	const q = `SELECT ` + branchColumns + `
		FROM branches
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`
	return scanBranch(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// Update writes the mutable columns of b and returns the refreshed row. code and
// organization_id are never changed here (identity/tenant). Returns db.ErrNoRows
// if the row is missing/deleted.
func (r *Repository) Update(ctx context.Context, b *Branch) (*Branch, error) {
	const q = `UPDATE branches SET
			name = $3, is_active = $4, is_default = $5,
			address = $6, city = $7, state = $8, postal_code = $9, country = $10,
			email = $11, phone = $12, latitude = $13, longitude = $14,
			open_time = $15, close_time = $16
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + branchColumns
	return scanBranch(r.db.FromCtx(ctx).QueryRow(ctx, q,
		b.ID, b.OrganizationID, b.Name, b.IsActive, b.IsDefault,
		b.Address, b.City, b.State, b.PostalCode, b.Country, b.Email, b.Phone,
		b.Latitude, b.Longitude, b.OpenTime, b.CloseTime,
	))
}

// SoftDelete marks the branch deleted (deleted_at = now()) within orgID and
// reports whether a row was affected (false → not found / already deleted). The
// partial unique index on (organization_id, code) excludes deleted rows, so the
// code becomes reusable immediately after deletion.
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `UPDATE branches SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ClearDefaultExcept unsets is_default on every non-deleted branch of orgID
// other than exceptID. Used inside the create/update transaction to preserve the
// "at most one default branch per organization" invariant. Pass uuid.Nil as
// exceptID to clear the flag on all branches.
func (r *Repository) ClearDefaultExcept(ctx context.Context, orgID, exceptID uuid.UUID) error {
	const q = `UPDATE branches SET is_default = FALSE
		WHERE organization_id = $1 AND id <> $2 AND is_default = TRUE AND deleted_at IS NULL`
	_, err := r.db.FromCtx(ctx).Exec(ctx, q, orgID, exceptID)
	return err
}

// CountExcept returns how many non-deleted branches other than exceptID exist
// in orgID. The service uses it to refuse deleting the last branch.
func (r *Repository) CountExcept(ctx context.Context, orgID, exceptID uuid.UUID) (int64, error) {
	const q = `SELECT count(*) FROM branches
		WHERE organization_id = $1 AND id <> $2 AND deleted_at IS NULL`
	var n int64
	err := r.db.FromCtx(ctx).QueryRow(ctx, q, orgID, exceptID).Scan(&n)
	return n, err
}

// listSortColumns is the allow-list of columns GET /branches may sort by. Any
// other "sort=" value is rejected, so the ORDER BY can never be built from raw
// client input (SQL-injection safe by construction).
var listSortColumns = map[string]struct{}{
	"code":       {},
	"name":       {},
	"created_at": {},
	"is_active":  {},
}

// List returns a filtered, paginated page of branches for orgID plus the total
// count matching the same filters (for pagination meta). Filters are optional:
// isActive (nil = any), city (exact, case-insensitive), q (substring of code or
// name). All values are passed as bind parameters.
func (r *Repository) List(ctx context.Context, orgID uuid.UUID, f ListFilter, o pagination.Offset) ([]ListItem, int64, error) {
	where := []string{"organization_id = $1", "deleted_at IS NULL"}
	args := []any{orgID}

	if f.IsActive != nil {
		args = append(args, *f.IsActive)
		where = append(where, "is_active = $"+strconv.Itoa(len(args)))
	}
	if f.City != "" {
		args = append(args, f.City)
		where = append(where, "lower(city) = lower($"+strconv.Itoa(len(args))+")")
	}
	if f.Q != "" {
		args = append(args, "%"+f.Q+"%")
		p := "$" + strconv.Itoa(len(args))
		where = append(where, "(code ILIKE "+p+" OR name ILIKE "+p+")")
	}
	whereSQL := strings.Join(where, " AND ")

	// Count first (same filters, no paging) for the pagination meta.
	var total int64
	countSQL := `SELECT count(*) FROM branches WHERE ` + whereSQL
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []ListItem{}, 0, nil
	}

	orderSQL := buildOrderBy(o.Sort)

	// Page args: LIMIT and OFFSET occupy the next two placeholders.
	args = append(args, o.Limit, o.OffsetSQL())
	listSQL := `SELECT id, code, name, is_active, is_default, city, phone
		FROM branches
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
		if err := rows.Scan(&it.ID, &it.Code, &it.Name, &it.IsActive, &it.IsDefault, &it.City, &it.Phone); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListFilter carries the optional filters for List (decoupled from the HTTP DTO
// so the repository does not import the transport layer's query struct shape).
type ListFilter struct {
	IsActive *bool
	City     string
	Q        string
}

// buildOrderBy turns a validated "sort=" string into an ORDER BY clause using
// only allow-listed columns. It falls back to a deterministic default so paging
// is stable, and always appends id as a tie-breaker for a total order.
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
