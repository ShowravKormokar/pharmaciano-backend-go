package organization

import "github.com/google/uuid"

// UpdateOrganizationRequest is the body of PATCH /organizations/{id}. Every
// field is optional (a pointer): a nil field is left unchanged, a non-nil field
// is written. Identity fields (id, slug) and lifecycle/billing fields
// (is_active, subscription_plan) are deliberately not updatable here — plan and
// activation changes belong to dedicated billing/admin flows, not the general
// "edit details" endpoint.
//
// Note on clearing: because absent and JSON null both decode to a nil pointer,
// this endpoint can set/replace a value but cannot null out an optional column;
// send an empty string to blank a free-text field.
type UpdateOrganizationRequest struct {
	Name              *string `json:"name"                validate:"omitempty,min=2,max=200"`
	TradeLicenseNo    *string `json:"trade_license_no"    validate:"omitempty,max=80"`
	DrugLicenseNo     *string `json:"drug_license_no"     validate:"omitempty,max=80"`
	VATRegistrationNo *string `json:"vat_registration_no" validate:"omitempty,max=80"`
	TIN               *string `json:"tin"                 validate:"omitempty,max=80"`

	ContactPhone *string `json:"contact_phone" validate:"omitempty,max=30"`
	ContactEmail *string `json:"contact_email" validate:"omitempty,email,max=255"`
	Website      *string `json:"website"       validate:"omitempty,url,max=255"`
	LogoURL      *string `json:"logo_url"      validate:"omitempty,url,max=500"`

	AddressLine1 *string `json:"address_line1" validate:"omitempty,max=255"`
	AddressLine2 *string `json:"address_line2" validate:"omitempty,max=255"`
	City         *string `json:"city"          validate:"omitempty,max=100"`
	State        *string `json:"state"         validate:"omitempty,max=100"`
	PostalCode   *string `json:"postal_code"   validate:"omitempty,max=20"`
	Country      *string `json:"country"       validate:"omitempty,max=100"`

	Currency *string `json:"currency" validate:"omitempty,iso_currency"`
	Timezone *string `json:"timezone" validate:"omitempty,iana_tz"`
}

// IsEmpty reports whether the request carries no changes at all, letting the
// service short-circuit to a plain read instead of issuing a no-op UPDATE.
func (r UpdateOrganizationRequest) IsEmpty() bool {
	return r == UpdateOrganizationRequest{}
}

// SummaryResponse is the body of GET /organizations/{id}/summary: lightweight
// tenant usage counters plus the current plan/activation, for dashboards.
type SummaryResponse struct {
	OrganizationID   uuid.UUID `json:"organization_id"`
	Name             string    `json:"name"`
	SubscriptionPlan string    `json:"subscription_plan"`
	IsActive         bool      `json:"is_active"`
	BranchCount      int64     `json:"branch_count"`
	UserCount        int64     `json:"user_count"`
}
