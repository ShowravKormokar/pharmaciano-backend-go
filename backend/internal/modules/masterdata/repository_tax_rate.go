package masterdata

import (
	"context"

	"github.com/google/uuid"

	"backend/pkg/pagination"
)

// taxRateColumns mirrors the declaration order of 000005_master_data.up.sql:89-99.
const taxRateColumns = `id, code, name, rate_percent, is_default, is_active, ` +
	`created_at, updated_at, deleted_at`

// taxRateTable describes tax_rates.
//
// NUMERIC: rate_percent is NUMERIC(6,3) and scans into a float64. See the
// conversion_factor note on unitTypeTable about float64 precision.
//
// is_default is sortable, but "exactly one row is_default" is NOT enforced here or
// anywhere: contract C-08 records the rule as undecided, including which row wins a
// conflict. The service enforces what it can once the owner settles it; until then
// this table stores whatever it is told, so more than one row may carry the flag.
var taxRateTable = tableSpec{
	table:        "tax_rates",
	columns:      taxRateColumns,
	insertCols:   `code, name, rate_percent, is_default, is_active`,
	updateCols:   `name, rate_percent, is_default, is_active`,
	sortColumns:  taxRateSortColumns,
	defaultOrder: defaultOrder,
}

// scanTaxRate maps one row into a TaxRate.
func scanTaxRate(row rowScanner) (*TaxRate, error) {
	var t TaxRate
	if err := row.Scan(
		&t.ID, &t.Code, &t.Name, &t.RatePercent, &t.IsDefault,
		&t.IsActive, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

// taxRateValues binds t as [id, code, then every mutable column].
func taxRateValues(t *TaxRate) []any {
	return []any{t.ID, t.Code, t.Name, t.RatePercent, t.IsDefault, t.IsActive}
}

// CreateTaxRate inserts a tax rate and returns the stored row. A duplicate code
// surfaces as a unique violation from ux_tax_rates_code.
//
// rate_percent is NOT NULL with no database default, so the service must always
// resolve it — CreateTaxRateRequest requires it for that reason.
func (r *Repository) CreateTaxRate(ctx context.Context, t *TaxRate) (*TaxRate, error) {
	return createRow(ctx, r, taxRateTable, t, taxRateValues, scanTaxRate)
}

// GetTaxRate returns the live tax rate with the given id, or db.ErrNoRows.
func (r *Repository) GetTaxRate(ctx context.Context, id uuid.UUID) (*TaxRate, error) {
	return getRow(ctx, r, taxRateTable, id, scanTaxRate, false)
}

// GetTaxRateForUpdate is GetTaxRate with a row lock.
func (r *Repository) GetTaxRateForUpdate(ctx context.Context, id uuid.UUID) (*TaxRate, error) {
	return getRow(ctx, r, taxRateTable, id, scanTaxRate, true)
}

// ListTaxRates returns a filtered, paginated page plus the total count.
func (r *Repository) ListTaxRates(ctx context.Context, f ListFilter, o pagination.Offset) ([]TaxRate, int64, error) {
	return listRows(ctx, r, taxRateTable, scanTaxRate, f, o)
}

// UpdateTaxRate writes the mutable columns of t and returns the refreshed row. code
// and id are never written.
func (r *Repository) UpdateTaxRate(ctx context.Context, t *TaxRate) (*TaxRate, error) {
	return updateRow(ctx, r, taxRateTable, t, taxRateValues, scanTaxRate)
}

// SoftDeleteTaxRate marks the tax rate deleted and reports whether a row was
// affected.
func (r *Repository) SoftDeleteTaxRate(ctx context.Context, id uuid.UUID) (bool, error) {
	return softDelete(ctx, r, taxRateTable, id)
}

// IsTaxRateReferenced reports whether any live medicine still points at this tax
// rate via medicines.tax_rate_id (000006:31).
func (r *Repository) IsTaxRateReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	return referencedByMedicine(ctx, r, "tax_rate_id", id)
}
