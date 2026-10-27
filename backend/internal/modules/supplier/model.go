package supplier

import "backend/internal/common"

// Supplier maps the `suppliers` table created in
// migrations/000005_master_data.up.sql, extended to the tenant-safe target
// schema described in SUPPLIER_CONTRACT.md §5.1.
//
// Migration dependency (§4.3): the deployed `suppliers` table has NO
// organization_id column. This struct therefore targets the schema AFTER a
// forward migration adds `organization_id UUID NOT NULL` plus a safe backfill.
// Do not run supplier tenant APIs against the un-migrated table — every
// repository query filters on organization_id, so it would fail or, worse,
// silently return cross-tenant rows.
//
// Per §6, OpeningBalance and CurrentBalance are NOT Supplier-owned financial
// truth. They are deliberately absent from this model; payables must be read
// through the Ledger module. Their eventual column retirement is a separate
// migration task after Ledger reconciliation is signed off.
type Supplier struct {
	common.BaseModel
	OrganizationID     string   `db:"organization_id"  json:"organization_id"`
	Code               string   `db:"code"             json:"code"`
	Name               string   `db:"name"             json:"name"`
	ContactPerson      *string  `db:"contact_person"   json:"contact_person,omitempty"`
	Phone              *string  `db:"phone"            json:"phone,omitempty"`
	Email              *string  `db:"email"            json:"email,omitempty"`
	Address            *string  `db:"address"          json:"address,omitempty"`
	City               *string  `db:"city"             json:"city,omitempty"`
	Country            *string  `db:"country"          json:"country,omitempty"`
	DrugLicenseNo      *string  `db:"drug_license_no"  json:"drug_license_no,omitempty"`
	TIN                *string  `db:"tin"              json:"tin,omitempty"`
	CreditLimit        *float64 `db:"credit_limit"     json:"credit_limit,omitempty"`
	CreditPeriodDays   *int     `db:"credit_period_days" json:"credit_period_days,omitempty"`
	IsActive           bool     `db:"is_active"        json:"is_active"`
}