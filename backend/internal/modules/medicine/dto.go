// Transport DTOs for the Medicine module.
//
// The shape of this file is dictated by three sources that must stay
// consistent with one another:
//
//   - migrations/000006_medicine_brand_supplier.up.sql is the only authority
//     for column names, nullability and VARCHAR widths. Every length limit
//     below is transcribed from that migration.
//   - docs/adr/MEDICINE_CONTRACT.md defines the ownership boundary. Medicine
//     owns product/formulation identity, classification, presentation, catalog
//     regulatory flags, and the variant/barcode identities used for exact
//     lookup. It does not own price, acquisition cost or reorder policy
//     (contract §2, §17).
//   - the sibling modules (brand, masterdata, audit) define the conventions
//     reused here: pointer fields on PATCH bodies with an IsEmpty() check, an
//     explicit allow-list on every request, a separate response type so a
//     future column cannot silently become part of the public API, and
//     NormalizeAndValidate() []error for the rules that struct tags cannot
//     express.
//
// Four rules are load-bearing:
//
//  1. NO TENANT OR AUDIT FIELDS. The medicines table has no organization_id,
//     no branch_id and no created_by/updated_by column, so no DTO accepts one.
//     A medicine's tenant is the organization of its brand
//     (brands.organization_id, added by migrations/000024) and must be
//     resolved from trusted request context, never from a request body.
//
//  2. NO PRICE, COST OR REORDER FIELDS. mrp, default_cost,
//     min_stock_threshold and max_stock_threshold exist on the table, but
//     price belongs to pricing, acquisition cost to purchase/inventory
//     valuation and reorder thresholds to inventory policy (contract §2, §17).
//     Accepting them here would make Medicine the owner of facts it must not
//     own, so they are absent from the request and the response DTOs. The
//     columns remain in the schema and in model.go until the owning modules
//     write them.
//
//  3. NO PRICE-HISTORY DTO. GET /api/v1/medicines/{id}/price-history must
//     delegate to the approved Pricing interface and must stay blocked while
//     no such interface exists (contract §1.12, §8). medicine_price_history
//     therefore has no DTO here.
//
//  4. IDENTITY KEYS ARE WRITE-ONCE. sku and barcode are the exact-lookup keys
//     used by POS; brand_id carries the medicine's tenant scope. None of them
//     is updatable, because rewriting a lookup key or moving a product between
//     organizations silently reinterprets what historical stock, purchases and
//     sales referred to (contract §5.3, §10, §11).

package medicine

import (
	"strings"
	"time"

	"github.com/google/uuid"

	uuidx "backend/internal/platform/uuid"
	"backend/pkg/pagination"
)

// Schema limits transcribed from migrations/000006_medicine_brand_supplier.up.sql.
const (
	maxSKULen             = 60
	maxBarcodeLen         = 60
	maxNameLen            = 255
	maxDisplayNameLen     = 255
	maxDescriptionLen     = 4000
	maxStrengthLen        = 60
	maxPackSizeLen        = 60
	maxVariantNameLen     = 200
	maxImporterNameLen    = 200
	maxImportLicenseNoLen = 80
	maxCountryOfOriginLen = 100
	maxHSNCodeLen         = 40
	maxImageURLLen        = 500
	maxReasonLen          = 255
	maxSearchLen          = pagination.MaxQueryLen
)

// ---------------------------------------------------------------------------
// Field-level validation errors
// ---------------------------------------------------------------------------

// FieldError is a field-level validation failure produced by the DTO-level
// validators in this package — the rules that struct tags cannot express,
// such as "exactly one of barcode or sku" and the query-parameter checks.
//
// It mirrors the shared error envelope's field shape so the handler can map
// it onto the shared error type without echoing request values back.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message,omitempty"`
}

// Error implements error.
func (e *FieldError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "validation error on field " + e.Field
}

// newFieldError builds a field-level validation error.
func newFieldError(field, rule, message string) error {
	return &FieldError{Field: field, Rule: rule, Message: message}
}

// ---------------------------------------------------------------------------
// medicines — create / update
// ---------------------------------------------------------------------------

// CreateMedicineRequest is the body of POST /api/v1/medicines.
//
// It is an explicit allow-list: a field that is not declared here can never be
// written, which is what keeps mass assignment and tenant spoofing out of the
// catalogue (contract §11).
//
// Reference fields carry a UUID that is syntax-checked only. Whether the
// referenced row exists, is active and belongs to the caller's organization is
// a service-layer concern (contract §7).
//
// Note on strength and pack_size: both are VARCHAR display text on the
// current schema, not structured composition. The contract asks for numeric
// strength and composition rows (contract §5.1, §17); that is a forward
// migration, not something this DTO may pretend to have.
type CreateMedicineRequest struct {
	// SKU is the catalogue identifier of the product. It is required, unique
	// among non-deleted rows, and write-once (it is the exact POS lookup key).
	SKU string `json:"sku" validate:"required,not_blank,max=60"`

	// Barcode is the optional medicine-level barcode. Variant-level barcodes
	// (see CreateMedicineBarcodeRequest) are the authoritative identifiers;
	// this column is the legacy catalogue-level copy.
	Barcode *string `json:"barcode" validate:"omitempty,not_blank,max=60"`

	// Name is the product name shown in the catalogue.
	Name string `json:"name" validate:"required,not_blank,max=255"`

	// DisplayName is the optional label used on receipts and pick lists.
	DisplayName *string `json:"display_name" validate:"omitempty,max=255"`

	// Description is free text. The column is TEXT; the API bounds it so a
	// single request cannot carry an unbounded payload.
	Description *string `json:"description" validate:"omitempty,max=4000"`

	// GenericMedicineID points at the generic (ingredient) dictionary entry.
	// It is the single-ingredient stand-in only: a combination product needs
	// structured composition rows, which do not exist yet (contract §5.1).
	GenericMedicineID *uuid.UUID `json:"generic_medicine_id" validate:"omitempty"`

	// BrandID points at the brand. It is required, and it is also what
	// carries the medicine's tenant scope (brands.organization_id).
	BrandID uuid.UUID `json:"brand_id" validate:"required"`

	// CategoryID points at the product category. One category per product on
	// the current schema; a many-to-many mapping would be a schema decision
	// (contract §6, "Category cardinality").
	CategoryID uuid.UUID `json:"category_id" validate:"required"`

	// DosageFormID points at the dosage-form dictionary entry.
	DosageFormID *uuid.UUID `json:"dosage_form_id" validate:"omitempty"`

	// RouteID points at the administration-route dictionary entry.
	RouteID *uuid.UUID `json:"route_id" validate:"omitempty"`

	// ManufacturerID points at the manufacturer. The brand already references
	// one, so this column is a denormalized duplicate: the service must
	// reject a value that contradicts brands.manufacturer_id rather than let
	// two sources of truth disagree (contract §17).
	ManufacturerID *uuid.UUID `json:"manufacturer_id" validate:"omitempty"`

	// StorageConditionID points at the storage-condition dictionary entry. It
	// is the preferred home of the refrigeration rule; IsRefrigerated is the
	// flat fallback (contract §5.4, §17).
	StorageConditionID *uuid.UUID `json:"storage_condition_id" validate:"omitempty"`

	// Strength is display text such as "500 mg". The custom `strength` rule
	// accepts the common pharma notations and rejects free text, because a
	// strength that cannot be parsed later cannot be converted safely.
	Strength *string `json:"strength" validate:"omitempty,not_blank,strength,max=60"`

	// PackSize is display text such as "10x10".
	PackSize *string `json:"pack_size" validate:"omitempty,not_blank,max=60"`

	// UnitsPerPack is the exact quantity contained in one package.
	UnitsPerPack *int `json:"units_per_pack" validate:"omitempty,gt=0"`

	// UnitsPerStrip is the exact quantity contained in one strip.
	UnitsPerStrip *int `json:"units_per_strip" validate:"omitempty,gt=0"`

	// PackageTypeID points at the package-type dictionary entry.
	PackageTypeID *uuid.UUID `json:"package_type_id" validate:"omitempty"`

	// SalesUnitID points at the unit-type dictionary entry used when selling.
	SalesUnitID *uuid.UUID `json:"sales_unit_id" validate:"omitempty"`

	// RequiresPrescription is a flat catalog flag. It documents that the
	// product may only be dispensed against a prescription; it does not
	// replace the prescription workflow (contract §5.4).
	RequiresPrescription *bool `json:"requires_prescription" validate:"omitempty"`

	// IsControlled marks a controlled product. Flat and single-jurisdiction:
	// it is not the final legal status of every product in every jurisdiction
	// (contract §5.4).
	IsControlled *bool `json:"is_controlled" validate:"omitempty"`

	// IsRefrigerated marks a cold-chain product. Prefer StorageConditionID
	// when it expresses the rule, so there is only one source of truth
	// (contract §17).
	IsRefrigerated *bool `json:"is_refrigerated" validate:"omitempty"`

	// IsImported marks an imported product.
	IsImported *bool `json:"is_imported" validate:"omitempty"`

	// CountryOfOrigin is the country of manufacture.
	CountryOfOrigin *string `json:"country_of_origin" validate:"omitempty,max=100"`

	// ImporterName is the importer of record. It is a plain name, NOT a
	// supplier reference: a supplier is a purchasing relationship and never
	// implies a manufacturer (contract §5.4).
	ImporterName *string `json:"importer_name" validate:"omitempty,max=200"`

	// ImportLicenseNo is the import-licence reference.
	ImportLicenseNo *string `json:"import_license_no" validate:"omitempty,max=80"`

	// TaxRateID points at the tax-rate dictionary entry. Only the reference
	// lives here; the tax rule belongs to the tax/sales contract (contract §17).
	TaxRateID *uuid.UUID `json:"tax_rate_id" validate:"omitempty"`

	// HSNCode is the reporting/tax classification code.
	HSNCode *string `json:"hsn_code" validate:"omitempty,max=40"`

	// ShelfLifeMonths is the catalogue default for shelf life. It is NOT a
	// batch expiry and never substitutes for one (contract §17).
	ShelfLifeMonths *int `json:"shelf_life_months" validate:"omitempty,gt=0"`

	// ImageURL is an optional media reference. Only an http(s) URL is
	// accepted, and only a URL the approved media rules allow (contract §17).
	ImageURL *string `json:"image_url" validate:"omitempty,http_url,max=500"`

	// IsActive defaults to TRUE in the schema when omitted.
	IsActive *bool `json:"is_active" validate:"omitempty"`

	// InitialVariants are the package variants that are created together with
	// the medicine, in the same transaction (contract §8, §10).
	//
	// They are optional: a variant can also be added later through
	// POST /api/v1/medicines/{id}/variants. The inline form exists because a
	// product that is not sellable without its packages is the normal case,
	// and a half-created product — a medicine with no package identity — is
	// not a usable catalogue entry.
	InitialVariants []CreateMedicineVariantRequest `json:"initial_variants" validate:"omitempty,dive"`
}

// UpdateMedicineRequest is the body of PATCH /api/v1/medicines/{id}.
//
// Every field is a pointer: a nil pointer means "leave unchanged", so an
// omitted field is never confused with an explicit zero, false or empty
// value. An explicit JSON null decodes to nil and is therefore also treated
// as "leave unchanged".
//
// Fields that are deliberately absent:
//
//   - sku, barcode — exact-lookup keys; changing them reinterprets history.
//   - brand_id — carries the tenant scope; changing it would move the product
//     between organizations.
//   - mrp, default_cost, min_stock_threshold, max_stock_threshold — owned by
//     pricing, purchase/inventory and inventory policy (contract §17).
//
// SKU-level, uniqueness and reference checks, the protection of semantic
// fields once the product has been referenced by stock, purchases or sales,
// and the audit/revision trail are service-layer responsibilities (contract
// §9, §10).
type UpdateMedicineRequest struct {
	Name        *string `json:"name"         validate:"omitempty,not_blank,max=255"`
	DisplayName *string `json:"display_name" validate:"omitempty,max=255"`
	Description *string `json:"description"  validate:"omitempty,max=4000"`

	// GenericMedicineID re-points the product at a generic dictionary entry.
	GenericMedicineID *uuid.UUID `json:"generic_medicine_id" validate:"omitempty"`

	// CategoryID re-points the product at a product category.
	CategoryID *uuid.UUID `json:"category_id" validate:"omitempty"`

	// DosageFormID re-points the product at a dosage-form dictionary entry.
	DosageFormID *uuid.UUID `json:"dosage_form_id" validate:"omitempty"`

	// RouteID re-points the product at an administration-route entry.
	RouteID *uuid.UUID `json:"route_id" validate:"omitempty"`

	// ManufacturerID re-points the product at a manufacturer. The service must
	// reject a value that contradicts the brand's manufacturer.
	ManufacturerID *uuid.UUID `json:"manufacturer_id" validate:"omitempty"`

	// StorageConditionID re-points the product at a storage condition.
	StorageConditionID *uuid.UUID `json:"storage_condition_id" validate:"omitempty"`

	// Strength and PackSize are display text on the current schema. The
	// service must reject a change to them once the product is referenced by
	// operational records (contract §5.1, §10).
	Strength      *string `json:"strength"        validate:"omitempty,not_blank,strength,max=60"`
	PackSize      *string `json:"pack_size"       validate:"omitempty,not_blank,max=60"`
	UnitsPerPack  *int    `json:"units_per_pack"  validate:"omitempty,gt=0"`
	UnitsPerStrip *int    `json:"units_per_strip" validate:"omitempty,gt=0"`

	// PackageTypeID and SalesUnitID re-point the packaging references.
	PackageTypeID *uuid.UUID `json:"package_type_id" validate:"omitempty"`
	SalesUnitID   *uuid.UUID `json:"sales_unit_id"   validate:"omitempty"`

	// The regulatory flags below are flat, single-jurisdiction flags. A
	// change to any of them requires elevated authorization, a reason, audit
	// evidence and a revision (contract §5.4, §11): the service must require
	// Reason when one of them changes.
	RequiresPrescription *bool `json:"requires_prescription" validate:"omitempty"`
	IsControlled         *bool `json:"is_controlled"         validate:"omitempty"`
	IsRefrigerated       *bool `json:"is_refrigerated"       validate:"omitempty"`
	IsImported           *bool `json:"is_imported"           validate:"omitempty"`

	// Import provenance.
	CountryOfOrigin *string `json:"country_of_origin" validate:"omitempty,max=100"`
	ImporterName    *string `json:"importer_name"     validate:"omitempty,max=200"`
	ImportLicenseNo *string `json:"import_license_no" validate:"omitempty,max=80"`

	// TaxRateID carries a dictionary reference only; the tax rule is owned by
	// the tax/sales contract.
	TaxRateID *uuid.UUID `json:"tax_rate_id" validate:"omitempty"`
	HSNCode   *string    `json:"hsn_code"    validate:"omitempty,max=40"`

	// ShelfLifeMonths is the catalogue default, not a batch expiry.
	ShelfLifeMonths *int `json:"shelf_life_months" validate:"omitempty,gt=0"`

	// ImageURL is an optional http(s) media reference.
	ImageURL *string `json:"image_url" validate:"omitempty,http_url,max=500"`

	// IsActive activates or deactivates the catalogue entry. Deactivation is
	// preferred over deletion: history keeps its references (contract §7).
	IsActive *bool `json:"is_active" validate:"omitempty"`

	// Reason documents the change. The service must require it whenever a
	// regulatory flag or the import provenance changes, because a regulatory
	// change needs a reason and audit evidence (contract §5.4, §11).
	Reason *string `json:"reason" validate:"omitempty,not_blank,max=255"`
}

// IsEmpty reports whether the PATCH carries no changes, letting the service
// short-circuit to a plain read instead of a no-op UPDATE.
func (r UpdateMedicineRequest) IsEmpty() bool {
	return r == UpdateMedicineRequest{}
}

// ---------------------------------------------------------------------------
// medicines — list and search
// ---------------------------------------------------------------------------

// ListMedicinesQuery binds the query string of GET /api/v1/medicines.
//
// Filters are the ones the contract names: category_id, brand_id, generic_id,
// is_active, requires_prescription, is_controlled, q, plus the shared
// pagination (contract §8.2).
//
// The boolean filters are pointers so an absent filter is never confused with
// an explicit false.
//
// The UUID filters are STRINGS, deliberately. gin's form binder has no case
// for [16]byte (which is what uuid.UUID is), so a `form:"brand_id"` UUID
// field fails to bind at all. The raw value is parsed through uuidx.ParsePointer
// — the same approach pkg/pagination/filter.go uses for ?branch_id= — and
// NormalizeAndValidate reports a malformed value as a field error. Call
// NormalizeAndValidate before using the parsed accessors.
type ListMedicinesQuery struct {
	pagination.Offset

	// CategoryID filters by product category.
	CategoryID string `form:"category_id" validate:"omitempty,max=36"`

	// BrandID filters by brand. Because a medicine's tenant is its brand's
	// organization, this filter is also the cheapest tenant-scoping lever
	// available on the current schema.
	BrandID string `form:"brand_id" validate:"omitempty,max=36"`

	// GenericID filters by generic (ingredient) dictionary entry.
	GenericID string `form:"generic_id" validate:"omitempty,max=36"`

	// IsActive filters on catalogue state.
	IsActive *bool `form:"is_active" validate:"omitempty"`

	// RequiresPrescription filters on the flat prescription flag.
	RequiresPrescription *bool `form:"requires_prescription" validate:"omitempty"`

	// IsControlled filters on the flat controlled flag.
	IsControlled *bool `form:"is_controlled" validate:"omitempty"`

	// Q is a bounded free-text term over the catalogue name, display name,
	// SKU and barcode.
	Q *string `form:"q" validate:"omitempty,max=200"`

	// Cursor switches the endpoint from offset pagination to keyset
	// pagination. It is the opaque, signed continuation token of the previous
	// page; a request without it returns the first offset page together with
	// the total count.
	//
	// The token is bound to the tenant and to the filter set, so a cursor
	// issued for one query is rejected by another (contract §8.2).
	Cursor string `form:"cursor" validate:"omitempty,max=512"`

	categoryID *uuid.UUID
	brandID    *uuid.UUID
	genericID  *uuid.UUID
}

// allowedMedicineSortFields is the allow-list for ?sort= on the medicine
// endpoints. A client may never supply a raw SQL expression (contract §8.2,
// §11); the repository appends id as the unique tie-breaker so the ordering
// is deterministic across pages.
var allowedMedicineSortFields = map[string]struct{}{
	"name":         {},
	"display_name": {},
	"sku":          {},
	"created_at":   {},
	"updated_at":   {},
}

// NormalizeAndValidate normalizes the filters, parses the UUID filters and
// checks the sort key. It returns every problem it finds rather than only
// the first one, so a client can fix a malformed request in one round trip.
func (q *ListMedicinesQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{newFieldError("query", "required", "query is required")}
	}
	var errs []error

	q.categoryID = nil
	q.brandID = nil
	q.genericID = nil

	if id, err := uuidx.ParsePointer(q.CategoryID); err != nil {
		errs = append(errs, newFieldError("category_id", "uuid", "must be a valid UUID"))
	} else {
		q.categoryID = id
	}

	if id, err := uuidx.ParsePointer(q.BrandID); err != nil {
		errs = append(errs, newFieldError("brand_id", "uuid", "must be a valid UUID"))
	} else {
		q.brandID = id
	}

	if id, err := uuidx.ParsePointer(q.GenericID); err != nil {
		errs = append(errs, newFieldError("generic_id", "uuid", "must be a valid UUID"))
	} else {
		q.genericID = id
	}

	// The search term is validated, not silently shortened: a term longer than
	// the bound is a malformed request, not a request for a truncated search.
	if len([]rune(trimmedTerm(q.Q))) > maxSearchLen {
		errs = append(errs, newFieldError("q", "max", "exceeds the maximum search length"))
	}

	if _, err := pagination.ParseSort(q.Sort, allowedMedicineSortFields); err != nil {
		errs = append(errs, newFieldError("sort", "oneof", "unsupported sort field"))
	}

	q.Normalize()
	return errs
}

// CategoryUUID returns the parsed ?category_id= filter, or nil for "no
// filter". Call NormalizeAndValidate first.
func (q ListMedicinesQuery) CategoryUUID() *uuid.UUID { return q.categoryID }

// BrandUUID returns the parsed ?brand_id= filter, or nil for "no filter".
// Call NormalizeAndValidate first.
func (q ListMedicinesQuery) BrandUUID() *uuid.UUID { return q.brandID }

// GenericUUID returns the parsed ?generic_id= filter, or nil for "no filter".
// Call NormalizeAndValidate first.
func (q ListMedicinesQuery) GenericUUID() *uuid.UUID { return q.genericID }

// NormalizedQ returns the ?q= term trimmed of surrounding whitespace and
// truncated to the maximum search length — exactly what pagination.Filter does
// for the shared ?q= parameter.
//
// The truncation is a defensive bound on the value handed to the repository;
// NormalizeAndValidate rejects an over-long term outright, so a request that
// reaches the repository never carries a shortened search.
//
// Case is deliberately NOT folded: the catalogue indexes name and display_name
// with trigram operators that serve ILIKE, so matching is already
// case-insensitive in the database and lowercasing here would only make the
// DTO disagree with the value echoed back to the caller.
func (q ListMedicinesQuery) NormalizedQ() string {
	term := trimmedTerm(q.Q)
	if len(term) > maxSearchLen {
		term = term[:maxSearchLen]
	}
	return term
}

// trimmedTerm returns the trimmed search term, or "" when it is absent.
func trimmedTerm(term *string) string {
	if term == nil {
		return ""
	}
	return strings.TrimSpace(*term)
}

// SearchMedicinesQuery binds the query string of
// GET /api/v1/medicines/search?q=.
//
// Search is bounded text matching, not an exact lookup and not a clinical
// substitution aid (contract §1.9, §7). The term is required: matching
// everything is the list endpoint's job, and an unbounded search term is a
// cheap way to force an expensive trigram scan.
type SearchMedicinesQuery struct {
	pagination.Offset

	// Q is the required search term.
	Q *string `form:"q" validate:"omitempty,max=200"`

	// Cursor is the opaque, signed continuation token of the previous page.
	// Search is always keyset paginated: a search result set is unbounded, so
	// an offset page would make the database count rows it throws away
	// (contract §8.2).
	Cursor string `form:"cursor" validate:"omitempty,max=512"`

	normalized string
}

// NormalizeAndValidate normalizes the search term and the pagination.
func (q *SearchMedicinesQuery) NormalizeAndValidate() []error {
	if q == nil {
		return []error{newFieldError("query", "required", "query is required")}
	}
	var errs []error

	q.normalized = ""
	if q.Q != nil {
		q.normalized = strings.TrimSpace(*q.Q)
	}
	if q.normalized == "" {
		errs = append(errs, newFieldError("q", "required", "a search term is required"))
	}
	if len([]rune(q.normalized)) > maxSearchLen {
		errs = append(errs, newFieldError("q", "max", "exceeds the maximum search length"))
	}

	if _, err := pagination.ParseSort(q.Sort, allowedMedicineSortFields); err != nil {
		errs = append(errs, newFieldError("sort", "oneof", "unsupported sort field"))
	}

	q.Normalize()
	return errs
}

// NormalizedQ returns the trimmed, length-bounded search term. Call
// NormalizeAndValidate first.
func (q SearchMedicinesQuery) NormalizedQ() string {
	if len(q.normalized) > maxSearchLen {
		return q.normalized[:maxSearchLen]
	}
	return q.normalized
}

// ---------------------------------------------------------------------------
// medicines — exact lookup
// ---------------------------------------------------------------------------

// LookupMedicineRequest is the body of POST /api/v1/medicines/lookup.
//
// The contract requires exactly one of barcode or sku (contract §8.2): zero
// matches is a not-found, more than one active match is an
// integrity/ambiguity error, and inactive or deleted variants are not
// eligible for ordinary POS lookup. Validate enforces the "exactly one" rule
// before the service runs the lookup.
type LookupMedicineRequest struct {
	// Barcode is the barcode to resolve.
	Barcode *string `json:"barcode" validate:"omitempty,not_blank,max=60"`

	// SKU is the SKU to resolve.
	SKU *string `json:"sku" validate:"omitempty,not_blank,max=60"`
}

// Validate enforces the "exactly one of barcode or sku" rule.
func (r *LookupMedicineRequest) Validate() []error {
	if r == nil {
		return []error{newFieldError("request", "required", "lookup request is required")}
	}
	var errs []error

	hasBarcode := r.Barcode != nil && strings.TrimSpace(*r.Barcode) != ""
	hasSKU := r.SKU != nil && strings.TrimSpace(*r.SKU) != ""

	switch {
	case hasBarcode && hasSKU:
		errs = append(errs, newFieldError("barcode", "exclusive", "provide exactly one of barcode or sku"))
	case !hasBarcode && !hasSKU:
		errs = append(errs, newFieldError("barcode", "required", "provide exactly one of barcode or sku"))
	}
	return errs
}

// LookupMedicineResponse is the result of an exact lookup.
//
// It carries catalogue and variant metadata only. It deliberately does NOT
// carry stock availability and NOT a price: a catalogue lookup does not prove
// stock, and the sale workflow revalidates branch stock, price and safety
// state itself (contract §7, §8.2).
type LookupMedicineResponse struct {
	MedicineID uuid.UUID  `json:"medicine_id"`
	VariantID  *uuid.UUID `json:"variant_id,omitempty"`

	MedicineName string  `json:"medicine_name"`
	VariantName  *string `json:"variant_name,omitempty"`
	BrandName    *string `json:"brand_name,omitempty"`

	SKU          *string `json:"sku,omitempty"`
	Barcode      *string `json:"barcode,omitempty"`
	Strength     *string `json:"strength,omitempty"`
	PackSize     *string `json:"pack_size,omitempty"`
	UnitsPerPack *int    `json:"units_per_pack,omitempty"`

	RequiresPrescription bool `json:"requires_prescription"`
	IsControlled         bool `json:"is_controlled"`
	IsActive             bool `json:"is_active"`
}

// ---------------------------------------------------------------------------
// medicine_variants
// ---------------------------------------------------------------------------

// CreateMedicineVariantRequest is the body of
// POST /api/v1/medicines/{id}/variants.
//
// A variant is a package identity: "Napa 500 mg, 10 tablets" and
// "Napa 500 mg, 100 tablets" are two variants of one medicine with their own
// SKU and barcode (contract §1.4, §5.2).
//
// mrp is absent on purpose: the catalogue list-price copy is owned by pricing
// (contract §17).
type CreateMedicineVariantRequest struct {
	// Name is the package label, for example "10 tablets x 10 strips".
	Name string `json:"name" validate:"required,not_blank,max=200"`

	// SKU is the sellable/stockable identity of this package. It is required,
	// unique among non-deleted rows, and write-once.
	SKU string `json:"sku" validate:"required,not_blank,max=60"`

	// Barcode is the optional package-level barcode. The medicine_barcodes
	// table is the authoritative place for variant identifiers; this column is
	// the legacy copy on the variant row.
	Barcode *string `json:"barcode" validate:"omitempty,not_blank,max=60"`

	// Strength and PackSize are display text on the current schema.
	Strength *string `json:"strength"  validate:"omitempty,not_blank,strength,max=60"`
	PackSize *string `json:"pack_size" validate:"omitempty,not_blank,max=60"`

	// UnitsPerPack is the exact quantity contained in the package.
	UnitsPerPack *int `json:"units_per_pack" validate:"omitempty,gt=0"`

	// IsActive defaults to TRUE in the schema when omitted.
	IsActive *bool `json:"is_active" validate:"omitempty"`
}

// UpdateMedicineVariantRequest is the body of
// PATCH /api/v1/medicines/{id}/variants/{variant_id}.
//
// Every field is a pointer: nil means "leave unchanged".
//
// sku and barcode are absent because they are the variant's identity and
// exact-lookup keys: a barcode is never silently transferred between variants
// and a barcode matching more than one active variant is an integrity error
// that must fail closed (contract §5.3).
//
// strength, pack_size and units_per_pack ARE present because a package may be
// corrected while the variant is still unused; once the variant has been
// referenced by purchase, inventory movement or sale, the service must reject
// a conversion change and require a new variant instead (contract §5.2, §10).
type UpdateMedicineVariantRequest struct {
	Name         *string `json:"name"          validate:"omitempty,not_blank,max=200"`
	Strength     *string `json:"strength"      validate:"omitempty,not_blank,strength,max=60"`
	PackSize     *string `json:"pack_size"     validate:"omitempty,not_blank,max=60"`
	UnitsPerPack *int    `json:"units_per_pack" validate:"omitempty,gt=0"`
	IsActive     *bool   `json:"is_active"     validate:"omitempty"`
}

// IsEmpty reports whether the PATCH carries no changes.
func (r UpdateMedicineVariantRequest) IsEmpty() bool {
	return r == UpdateMedicineVariantRequest{}
}

// MedicineVariantResponse is the single-resource representation of a
// package variant.
type MedicineVariantResponse struct {
	ID         uuid.UUID `json:"id"`
	MedicineID uuid.UUID `json:"medicine_id"`

	Name         string  `json:"name"`
	SKU          string  `json:"sku"`
	Barcode      *string `json:"barcode,omitempty"`
	Strength     *string `json:"strength,omitempty"`
	PackSize     *string `json:"pack_size,omitempty"`
	UnitsPerPack *int    `json:"units_per_pack,omitempty"`

	IsActive bool `json:"is_active"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// medicine_barcodes
// ---------------------------------------------------------------------------

// CreateMedicineBarcodeRequest is the body of
// POST /api/v1/medicines/{id}/barcodes.
//
// A barcode belongs to a variant, not to the medicine alone (contract §5.3).
// VariantID is therefore the identifier that gives the code its meaning;
// the medicine-level barcode is a separate, legacy identifier.
type CreateMedicineBarcodeRequest struct {
	// VariantID attaches the barcode to a package variant.
	VariantID *uuid.UUID `json:"variant_id" validate:"omitempty"`

	// Barcode is the identifier. It must be exact and unique among
	// non-deleted rows; the database unique index is the final guard.
	Barcode string `json:"barcode" validate:"required,not_blank,max=60"`

	// BarcodeType is the symbology. It must be one of the values the schema
	// CHECK constraint allows; the column defaults to EAN13 when omitted.
	BarcodeType MedicineBarcodeType `json:"barcode_type" validate:"omitempty,oneof=EAN13 EAN8 UPC CODE128 QR OTHER"`

	// IsPrimary marks the preferred identifier of the variant.
	IsPrimary *bool `json:"is_primary" validate:"omitempty"`
}

// MedicineBarcodeResponse is the single-resource representation of a barcode
// assignment.
type MedicineBarcodeResponse struct {
	ID         uuid.UUID  `json:"id"`
	MedicineID uuid.UUID  `json:"medicine_id"`
	VariantID  *uuid.UUID `json:"variant_id,omitempty"`

	Barcode     string              `json:"barcode"`
	BarcodeType MedicineBarcodeType `json:"barcode_type"`
	IsPrimary   bool                `json:"is_primary"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// medicines — response
// ---------------------------------------------------------------------------

// MedicineReferenceLabels carries the joined display labels of the
// MasterData and Brand references a medicine points at.
//
// The labels are batch-loaded by the repository so that neither the detail
// endpoint nor the list endpoint performs a per-row lookup (contract §9,
// "Performance"). A nil *MedicineReferenceLabels means the caller did not
// ask for the joined view.
type MedicineReferenceLabels struct {
	BrandName    string `json:"brand_name"`
	CategoryName string `json:"category_name"`

	GenericName          *string `json:"generic_name,omitempty"`
	DosageFormName       *string `json:"dosage_form_name,omitempty"`
	RouteName            *string `json:"route_name,omitempty"`
	ManufacturerName     *string `json:"manufacturer_name,omitempty"`
	StorageConditionName *string `json:"storage_condition_name,omitempty"`
	PackageTypeName      *string `json:"package_type_name,omitempty"`
	SalesUnitName        *string `json:"sales_unit_name,omitempty"`
	TaxRateCode          *string `json:"tax_rate_code,omitempty"`
}

// MedicineResponse is the single-resource representation of a catalogue
// entry.
//
// It mirrors the public columns of the medicines table and nothing else:
// mrp, default_cost, min_stock_threshold and max_stock_threshold are
// deliberately absent because pricing, purchase/inventory and inventory
// policy own them (contract §2, §17). A future column cannot become part of
// the public API by accident, because this type is declared explicitly.
type MedicineResponse struct {
	ID      uuid.UUID `json:"id"`
	SKU     string    `json:"sku"`
	Barcode *string   `json:"barcode,omitempty"`

	Name        string  `json:"name"`
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`

	GenericMedicineID  *uuid.UUID `json:"generic_medicine_id,omitempty"`
	BrandID            uuid.UUID  `json:"brand_id"`
	CategoryID         uuid.UUID  `json:"category_id"`
	DosageFormID       *uuid.UUID `json:"dosage_form_id,omitempty"`
	RouteID            *uuid.UUID `json:"route_id,omitempty"`
	ManufacturerID     *uuid.UUID `json:"manufacturer_id,omitempty"`
	StorageConditionID *uuid.UUID `json:"storage_condition_id,omitempty"`

	Strength      *string    `json:"strength,omitempty"`
	PackSize      *string    `json:"pack_size,omitempty"`
	UnitsPerPack  *int       `json:"units_per_pack,omitempty"`
	UnitsPerStrip *int       `json:"units_per_strip,omitempty"`
	PackageTypeID *uuid.UUID `json:"package_type_id,omitempty"`
	SalesUnitID   *uuid.UUID `json:"sales_unit_id,omitempty"`

	RequiresPrescription bool    `json:"requires_prescription"`
	IsControlled         bool    `json:"is_controlled"`
	IsRefrigerated       bool    `json:"is_refrigerated"`
	IsImported           bool    `json:"is_imported"`
	CountryOfOrigin      *string `json:"country_of_origin,omitempty"`
	ImporterName         *string `json:"importer_name,omitempty"`
	ImportLicenseNo      *string `json:"import_license_no,omitempty"`

	TaxRateID *uuid.UUID `json:"tax_rate_id,omitempty"`
	HSNCode   *string    `json:"hsn_code,omitempty"`

	ShelfLifeMonths *int    `json:"shelf_life_months,omitempty"`
	ImageURL        *string `json:"image_url,omitempty"`

	IsActive bool `json:"is_active"`

	// References holds the joined dictionary labels, when the caller asked
	// for the joined view.
	References *MedicineReferenceLabels `json:"references,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
