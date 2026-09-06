package organization

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"backend/internal/platform/db"
)

// Repository is the organization data-access layer. It depends on *db.DB and
// reads/writes through db.ReadFromCtx / db.FromCtx, so every method
// transparently joins an ambient transaction when the service opens one and
// otherwise runs on the pool (reads may use the replica).
//
// Methods return raw driver errors (db.ErrNoRows, unique-violation, …); mapping
// them to domain *errs.AppError values is the service layer's job.
type Repository struct {
	db *db.DB
}

// NewRepository wires the repository to the shared database handle.
func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// organizationColumns is the canonical column order shared by every SELECT and
// the UPDATE ... RETURNING. scanOrganization Scans in exactly this order, so the
// two must always change together.
const organizationColumns = `id, name, slug, trade_license_no, drug_license_no, ` +
	`vat_registration_no, tin, subscription_plan, is_active, contact_phone, ` +
	`contact_email, website, logo_url, address_line1, address_line2, city, state, ` +
	`postal_code, country, currency, timezone, created_at, updated_at, deleted_at`

// scanOrganization maps one row (from Query or QueryRow) into an Organization.
func scanOrganization(row pgx.Row) (*Organization, error) {
	var o Organization
	if err := row.Scan(
		&o.ID, &o.Name, &o.Slug, &o.TradeLicenseNo, &o.DrugLicenseNo,
		&o.VATRegistrationNo, &o.TIN, &o.SubscriptionPlan, &o.IsActive, &o.ContactPhone,
		&o.ContactEmail, &o.Website, &o.LogoURL, &o.AddressLine1, &o.AddressLine2, &o.City, &o.State,
		&o.PostalCode, &o.Country, &o.Currency, &o.Timezone, &o.CreatedAt, &o.UpdatedAt, &o.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &o, nil
}

// FindByID returns the non-deleted organization with the given id. Returns
// db.ErrNoRows if it does not exist or is soft-deleted.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*Organization, error) {
	const q = `SELECT ` + organizationColumns + `
		FROM organizations
		WHERE id = $1 AND deleted_at IS NULL`
	return scanOrganization(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id))
}

// FindByIDForUpdate is FindByID with a row lock (SELECT ... FOR UPDATE). The
// service calls it inside a transaction so the read-modify-write of a partial
// PATCH is atomic and cannot lose a concurrent update. It always targets the
// primary (FromCtx), never the replica. Returns db.ErrNoRows if missing/deleted.
func (r *Repository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*Organization, error) {
	const q = `SELECT ` + organizationColumns + `
		FROM organizations
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`
	return scanOrganization(r.db.FromCtx(ctx).QueryRow(ctx, q, id))
}

// Update writes the mutable profile columns of o and returns the refreshed row
// (updated_at is bumped by the set_updated_at trigger). Identity (id, slug) and
// lifecycle/billing columns (is_active, subscription_plan) are intentionally
// not touched here. Returns db.ErrNoRows if the row is missing or soft-deleted.
func (r *Repository) Update(ctx context.Context, o *Organization) (*Organization, error) {
	const q = `UPDATE organizations SET
			name = $2, trade_license_no = $3, drug_license_no = $4,
			vat_registration_no = $5, tin = $6, contact_phone = $7, contact_email = $8,
			website = $9, logo_url = $10, address_line1 = $11, address_line2 = $12,
			city = $13, state = $14, postal_code = $15, country = $16,
			currency = $17, timezone = $18
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING ` + organizationColumns
	return scanOrganization(r.db.FromCtx(ctx).QueryRow(ctx, q,
		o.ID, o.Name, o.TradeLicenseNo, o.DrugLicenseNo, o.VATRegistrationNo, o.TIN,
		o.ContactPhone, o.ContactEmail, o.Website, o.LogoURL,
		o.AddressLine1, o.AddressLine2, o.City, o.State, o.PostalCode, o.Country,
		o.Currency, o.Timezone,
	))
}

// CountBranches returns the number of non-deleted branches in the organization.
func (r *Repository) CountBranches(ctx context.Context, orgID uuid.UUID) (int64, error) {
	const q = `SELECT count(*) FROM branches WHERE organization_id = $1 AND deleted_at IS NULL`
	var n int64
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID).Scan(&n)
	return n, err
}

// CountUsers returns the number of non-deleted users in the organization.
func (r *Repository) CountUsers(ctx context.Context, orgID uuid.UUID) (int64, error) {
	const q = `SELECT count(*) FROM users WHERE organization_id = $1 AND deleted_at IS NULL`
	var n int64
	err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, orgID).Scan(&n)
	return n, err
}
