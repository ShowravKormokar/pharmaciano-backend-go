// Package organization implements the organization (tenant root) module: the
// single row that represents the pharmacy business a set of users belongs to.
// Organizations are provisioned during onboarding (out of scope here); this
// module only reads and updates the caller's own organization and reports a
// small usage summary.
package organization

import "backend/internal/common"

// Organization is the tenant root. One row per pharmacy business; every other
// tenant-scoped table carries this row's id as organization_id.
//
// Field order and db tags mirror migrations/000002 (organizations) exactly so
// the repository can SELECT a fixed column list and Scan straight into this
// struct. Nullable columns are pointers.
type Organization struct {
	common.BaseModel

	Name              string  `db:"name"                json:"name"`
	Slug              string  `db:"slug"                json:"slug"` // unique, url-safe, immutable
	TradeLicenseNo    *string `db:"trade_license_no"    json:"trade_license_no,omitempty"`
	DrugLicenseNo     *string `db:"drug_license_no"     json:"drug_license_no,omitempty"`
	VATRegistrationNo *string `db:"vat_registration_no" json:"vat_registration_no,omitempty"`
	TIN               *string `db:"tin"                 json:"tin,omitempty"`
	SubscriptionPlan  string  `db:"subscription_plan"   json:"subscription_plan"` // free/pro/enterprise
	IsActive          bool    `db:"is_active"           json:"is_active"`

	ContactPhone *string `db:"contact_phone" json:"contact_phone,omitempty"`
	ContactEmail *string `db:"contact_email" json:"contact_email,omitempty"`
	Website      *string `db:"website"       json:"website,omitempty"`
	LogoURL      *string `db:"logo_url"      json:"logo_url,omitempty"`

	AddressLine1 *string `db:"address_line1" json:"address_line1,omitempty"`
	AddressLine2 *string `db:"address_line2" json:"address_line2,omitempty"`
	City         *string `db:"city"          json:"city,omitempty"`
	State        *string `db:"state"         json:"state,omitempty"`
	PostalCode   *string `db:"postal_code"   json:"postal_code,omitempty"`
	Country      *string `db:"country"       json:"country,omitempty"`

	Currency string `db:"currency" json:"currency"` // ISO-4217, default BDT
	Timezone string `db:"timezone" json:"timezone"` // IANA, e.g. Asia/Dhaka
}
