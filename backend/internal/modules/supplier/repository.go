package supplier

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

// Repository is the supplier data-access layer. Every method is tenant-scoped
// by organization_id per SUPPLIER_CONTRACT.md §3/§4. Methods return raw driver
// errors for the service to translate.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// supplierColumns is the canonical column order shared by every SELECT and the
// INSERT/UPDATE ... RETURNING. scanSupplier scans in exactly this order.
const supplierColumns = `id, organization_id, code, name, contact_person, phone, email, ` +
	`address, city, country, drug_license_no, tin, credit_limit, credit_period_days, ` +
	`is_active, created_at, updated_at, deleted_at`

// scanSupplier maps one row into a Supplier.
func scanSupplier(row pgx.Row) (*Supplier, error) {
	var s Supplier
	if err := row.Scan(
		&s.ID, &s.OrganizationID, &s.Code, &s.Name, &s.ContactPerson, &s.Phone, &s.Email,
		&s.Address, &s.City, &s.Country, &s.DrugLicenseNo, &s.TIN, &s.CreditLimit,
		&s.CreditPeriodDays, &s.IsActive, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &s, nil
}

// Insert creates a supplier and returns the stored row. organization_id must
// already be set on s by the service from trusted context. A duplicate code
// surfaces as a unique violation.
func (r *Repository) Insert(ctx context.Context, s *Supplier) (*Supplier, error) {
	const q = `INSERT INTO suppliers (
			organization_id, code, name, contact_person, phone, email, 
			address, city, country, drug_license_no, tin, 
			credit_limit, credit_period_days, is_active
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING ` + supplierColumns
	return scanSupplier(r.db.FromCtx(ctx).QueryRow(ctx, q,
		s.OrganizationID, s.Code, s.Name, s.ContactPerson, s.Phone, s.Email,
		s.Address, s.City, s.Country, s.DrugLicenseNo, s.TIN,
		s.CreditLimit, s.CreditPeriodDays, s.IsActive,
	))
}

// FindByID returns the non-deleted supplier with the given id within orgID.
// The org filter makes cross-tenant reads indistinguishable from missing.
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*Supplier, error) {
	const q = `SELECT ` + supplierColumns + `
		FROM suppliers
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanSupplier(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByIDForUpdate is FindByID with a row lock, for the read-modify-write of a
// PATCH/DELETE inside a transaction. Always targets the primary.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Supplier, error) {
	const q = `SELECT ` + supplierColumns + `
		FROM suppliers
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`
	return scanSupplier(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// Update writes the mutable columns of s and returns the refreshed row. code
// and organization_id are never changed here. Returns db.ErrNoRows if the row
// is missing/deleted.
func (r *Repository) Update(ctx context.Context, s *Supplier) (*Supplier, error) {
	const q = `UPDATE suppliers SET
			name = $3, contact_person = $4, phone = $5, email = $6,
			address = $7, city = $8, country = $9, drug_license_no = $10, tin = $11,
			credit_limit = $12, credit_period_days = $13, is_active = $14
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + supplierColumns
	return scanSupplier(r.db.FromCtx(ctx).QueryRow(ctx, q,
		s.ID, s.OrganizationID, s.Name, s.ContactPerson, s.Phone, s.Email,
		s.Address, s.City, s.Country, s.DrugLicenseNo, s.TIN,
		s.CreditLimit, s.CreditPeriodDays, s.IsActive,
	))
}

// SoftDelete marks the supplier deleted within orgID and reports whether a row
// was affected. The partial unique index on code excludes deleted rows, so the
// code becomes reusable immediately.
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `UPDATE suppliers SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// listSortColumns is the allow-list of columns the list endpoints may sort by.
var listSortColumns = map[string]struct{}{
	"code":       {},
	"name":       {},
	"city":       {},
	"created_at": {},
	"is_active":  {},
}

// ListFilter carries the optional filters for List.
type ListFilter struct {
	City     *string
	IsActive *bool
	Q        string
}

// List returns a filtered, paginated page of suppliers for orgID plus the total
// count matching the same filters. All values are passed as bind parameters.
func (r *Repository) List(ctx context.Context, orgID uuid.UUID, f ListFilter, o pagination.Offset) ([]ListItem, int64, error) {
	where := []string{"organization_id = $1", "deleted_at IS NULL"}
	args := []any{orgID}

	if f.City != nil {
		args = append(args, strings.TrimSpace(*f.City))
		where = append(where, "city = $"+strconv.Itoa(len(args)))
	}
	if f.IsActive != nil {
		args = append(args, *f.IsActive)
		where = append(where, "is_active = $"+strconv.Itoa(len(args)))
	}
	if term := strings.TrimSpace(f.Q); term != "" {
		if len(term) > pagination.MaxQueryLen {
			term = term[:pagination.MaxQueryLen]
		}
		args = append(args, "%"+escapeLike(term)+"%")
		where = append(where, "name ILIKE $"+strconv.Itoa(len(args))+" ESCAPE '\\'")
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	countSQL := `SELECT count(*) FROM suppliers WHERE ` + whereSQL
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []ListItem{}, 0, nil
	}

	orderSQL := buildOrderBy(o.Sort)
	args = append(args, o.Limit, o.OffsetSQL())
	listSQL := `SELECT id, code, name, city, country, credit_limit, credit_period_days, is_active, created_at, updated_at
		FROM suppliers
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
		if err := rows.Scan(&it.ID, &it.Code, &it.Name, &it.City, &it.Country, &it.CreditLimit, &it.CreditPeriodDays, &it.IsActive, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// buildOrderBy turns a validated "sort=" string into an ORDER BY clause using
// only allow-listed columns, with a deterministic default and an id tie-breaker.
func buildOrderBy(sort string) string {
	fields, err := pagination.ParseSort(sort, listSortColumns)
	if err != nil || len(fields) == 0 {
		return "ORDER BY name ASC, id ASC"
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