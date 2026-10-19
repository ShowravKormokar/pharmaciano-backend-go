package brand

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/pagination"
)

const brandColumns = `id, organization_id, code, name, manufacturer_id, country_of_origin, description, is_active, created_at, updated_at, deleted_at`

// Repository is the brand data-access layer. Methods return raw driver errors
// for the service to translate.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// rowScanner is the Scan subset shared by pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanBrand maps one row into a Brand.
func scanBrand(row rowScanner) (*Brand, error) {
	var v Brand
	if err := row.Scan(
		&v.ID, &v.OrganizationID, &v.Code, &v.Name, &v.ManufacturerID,
		&v.CountryOfOrigin, &v.Description, &v.IsActive,
		&v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// Create inserts a tenant-owned brand and returns the stored row.
func (r *Repository) Create(ctx context.Context, orgID uuid.UUID, v *Brand) (*Brand, error) {
	q := `INSERT INTO brands (organization_id, code, name, manufacturer_id, country_of_origin, description, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + brandColumns
	return scanBrand(r.db.FromCtx(ctx).QueryRow(ctx, q,
		orgID, v.Code, v.Name, v.ManufacturerID, v.CountryOfOrigin, v.Description, v.IsActive))
}

// ManufacturerIsEligible locks a live, active manufacturer belonging to orgID.
func (r *Repository) ManufacturerIsEligible(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	const q = `SELECT id FROM manufacturers
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND is_active = TRUE
		FOR SHARE`
	var found uuid.UUID
	if err := r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID).Scan(&found); err != nil {
		if err == db.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// FindByID returns a live brand belonging to orgID, or db.ErrNoRows.
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*Brand, error) {
	q := `SELECT ` + brandColumns + `
		FROM brands
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	return scanBrand(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// FindByIDForUpdate is FindByID with a row lock, for read-modify-write.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Brand, error) {
	q := `SELECT ` + brandColumns + `
		FROM brands
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		FOR UPDATE`
	return scanBrand(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
}

// List returns a filtered, paginated page plus the total count.
func (r *Repository) List(ctx context.Context, orgID uuid.UUID, f ListBrandsQuery, o pagination.Offset, sort []pagination.SortField) ([]Brand, int64, error) {
	orderBy, err := buildBrandOrderBy(sort)
	if err != nil {
		return nil, 0, err
	}
	where := []string{"organization_id = $1", "deleted_at IS NULL"}
	args := []any{orgID}

	if f.ManufacturerID != nil {
		args = append(args, *f.ManufacturerID)
		where = append(where, "manufacturer_id = $"+strconv.Itoa(len(args)))
	}
	if f.IsActive != nil {
		args = append(args, *f.IsActive)
		where = append(where, "is_active = $"+strconv.Itoa(len(args)))
	}
	term := normalizeQ(f.Q)
	if term != "" {
		args = append(args, likePattern(term))
		where = append(where, "(name ILIKE $"+strconv.Itoa(len(args))+" ESCAPE '!' OR code ILIKE $"+strconv.Itoa(len(args))+" ESCAPE '!')")
	}

	whereClause := strings.Join(where, " AND ")

	var total int64
	countSQL := `SELECT count(*) FROM brands WHERE ` + whereClause
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	o.Normalize()
	limit := o.Limit
	items := make([]Brand, 0, limit)
	if total == 0 {
		return items, 0, nil
	}

	offset := o.OffsetSQL()
	pageArgs := append(append([]any{}, args...), limit, offset)

	listSQL := `SELECT ` + brandColumns + `
		FROM brands
		WHERE ` + whereClause + `
		ORDER BY ` + orderBy + `
		LIMIT $` + strconv.Itoa(len(pageArgs)-1) + ` OFFSET $` + strconv.Itoa(len(pageArgs))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		item, err := scanBrand(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Update writes the mutable columns of v and returns the refreshed row.
func (r *Repository) Update(ctx context.Context, orgID uuid.UUID, v *Brand) (*Brand, error) {
	q := `UPDATE brands SET
		name = $3,
		manufacturer_id = $4,
		country_of_origin = $5,
		description = $6,
		is_active = $7
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING ` + brandColumns
	return scanBrand(r.db.FromCtx(ctx).QueryRow(ctx, q,
		v.ID, orgID, v.Name, v.ManufacturerID, v.CountryOfOrigin, v.Description, v.IsActive))
}

// SoftDelete marks the brand deleted and reports whether a row was affected.
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	q := `UPDATE brands SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// IsReferencedByMedicine reports whether any medicine row still points at this brand.
func (r *Repository) IsReferencedByMedicine(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	q := `SELECT EXISTS (
		SELECT 1
		FROM medicines m
		JOIN brands b ON b.id = m.brand_id
		WHERE b.organization_id = $1 AND b.id = $2
	)`
	var ok bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID, id).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// ---- helpers ----

func normalizeQ(q string) string {
	return strings.TrimSpace(q)
}

func likePattern(term string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return "%" + replacer.Replace(term) + "%"
}

func buildBrandOrderBy(sort []pagination.SortField) (string, error) {
	if len(sort) == 0 {
		return "code ASC, id ASC", nil
	}
	parts := make([]string, 0, len(sort)+1)
	hasID := false
	for _, field := range sort {
		if _, allowed := brandSortFields[field.Field]; !allowed {
			return "", fmt.Errorf("brand repository: unsupported sort field %q", field.Field)
		}
		direction := "ASC"
		if field.Desc {
			direction = "DESC"
		}
		parts = append(parts, field.Field+" "+direction)
		if field.Field == "id" {
			hasID = true
		}
	}
	if !hasID {
		parts = append(parts, "id ASC")
	}
	return strings.Join(parts, ", "), nil
}

var brandSortFields = map[string]struct{}{
	"code":       {},
	"name":       {},
	"created_at": {},
	"is_active":  {},
	"id":         {},
}
