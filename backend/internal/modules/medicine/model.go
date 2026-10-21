// Package medicine implements the medicine module: the central product
// catalogue that one organization shares across all of its branches.
//
// Scope follows MEDICINE_CONTRACT.md. Medicine owns product/formulation
// identity, the MasterData and Brand references a product points at, and the
// package variant and barcode identities used for POS lookup. It does not own
// branch/warehouse stock, acquisition cost, selling price, reorder policy, or
// import/export job execution — those belong to inventory, purchase, sale,
// pricing and the shared job framework.
//
// The types below are persistence projections of the tables created by
// migrations/000006_medicine_brand_supplier.up.sql. They map rows; they carry
// no SQL, no HTTP types, and no authorization rules.
//
// Tenancy: the medicines table has no organization_id column, so a product's
// tenant is the organization of the brand it references (brands.organization_id
// was added by migrations/000024, which also enforces that a medicine's brand
// is active). Every read that is not already brand-scoped must therefore
// resolve tenant scope through the brand, and a catalog-wide read needs that
// join rather than an assumed single-tenant table.
package medicine

import (
	"time"

	"github.com/google/uuid"

	"backend/internal/common"
)

// ---------------------------------------------------------------------------
// Constrained column types
// ---------------------------------------------------------------------------

// MedicineBarcodeType is the symbology recorded on a barcode row. The values
// mirror the CHECK constraint on medicine_barcodes.barcode_type
// (000006:78) and are not interchangeable: a UPC and an EAN13 are different
// identifiers even when the digits coincide.
type MedicineBarcodeType string

const (
	MedicineBarcodeTypeEAN13   MedicineBarcodeType = "EAN13"
	MedicineBarcodeTypeEAN8    MedicineBarcodeType = "EAN8"
	MedicineBarcodeTypeUPC     MedicineBarcodeType = "UPC"
	MedicineBarcodeTypeCODE128 MedicineBarcodeType = "CODE128"
	MedicineBarcodeTypeQR      MedicineBarcodeType = "QR"
	MedicineBarcodeTypeOther   MedicineBarcodeType = "OTHER"
)

// Valid reports whether the value satisfies the database CHECK constraint.
func (b MedicineBarcodeType) Valid() bool {
	switch b {
	case MedicineBarcodeTypeEAN13, MedicineBarcodeTypeEAN8, MedicineBarcodeTypeUPC,
		MedicineBarcodeTypeCODE128, MedicineBarcodeTypeQR, MedicineBarcodeTypeOther:
		return true
	}
	return false
}

// MedicinePriceType is the kind of price a price-history row records. The
// values mirror the CHECK constraint on medicine_price_history.price_type
// (000006:91).
//
// Price history is not a Medicine-owned fact: pricing owns the authoritative
// price books and history. This type exists only to read the pre-existing
// table, and the Medicine API must not expose price history until an approved
// Pricing interface exists to delegate to (contract §1.12, §17).
type MedicinePriceType string

const (
	MedicinePriceTypeMRP       MedicinePriceType = "mrp"
	MedicinePriceTypeWholesale MedicinePriceType = "wholesale"
	MedicinePriceTypeRetail    MedicinePriceType = "retail"
	MedicinePriceTypeCost      MedicinePriceType = "cost"
)

// Valid reports whether the value satisfies the database CHECK constraint.
func (p MedicinePriceType) Valid() bool {
	switch p {
	case MedicinePriceTypeMRP, MedicinePriceTypeWholesale, MedicinePriceTypeRetail, MedicinePriceTypeCost:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// medicines
// ---------------------------------------------------------------------------

// Medicine is one row of the central product catalogue.
//
// Field order and db tags mirror migrations/000006 (medicines) exactly so the
// repository can SELECT a fixed column list and Scan straight into this
// struct. Nullable columns are pointers; NOT NULL columns are values.
//
// common.BaseModel is embedded because the table carries id, created_at,
// updated_at and deleted_at and nothing else: there are no created_by or
// updated_by columns, so common.AuditColumns must not be used (contract §17).
//
// Scope notes, per contract §17:
//   - SKU, MRP and DefaultCost are on the table, so they are mapped. They are
//     not, however, authoritative: the catalog SKU identifies a product, while
//     price and cost are owned by pricing and purchase/inventory. Treat MRP and
//     DefaultCost as denormalized copies, never as a source of truth.
//   - Strength and PackSize are VARCHAR on the current schema. The contract
//     asks for structured numeric strength and composition rows; that is a new
//     migration, not a model change.
//   - ManufacturerID duplicates the manufacturer reachable through BrandID.
//     Keep it only while the column exists; do not present the two as
//     independent relationships.
type Medicine struct {
	common.BaseModel

	SKU     string  `db:"sku"     json:"sku"`               // unique among non-deleted rows
	Barcode *string `db:"barcode" json:"barcode,omitempty"` // unique among non-deleted rows when present

	Name        string  `db:"name"         json:"name"`
	DisplayName *string `db:"display_name" json:"display_name,omitempty"`
	Description *string `db:"description"  json:"description,omitempty"`

	// Classification
	GenericMedicineID  *uuid.UUID `db:"generic_medicine_id" json:"generic_medicine_id,omitempty"`
	BrandID            uuid.UUID  `db:"brand_id"            json:"brand_id"`
	CategoryID         uuid.UUID  `db:"category_id"         json:"category_id"`
	DosageFormID       *uuid.UUID `db:"dosage_form_id"      json:"dosage_form_id,omitempty"`
	RouteID            *uuid.UUID `db:"route_id"            json:"route_id,omitempty"`
	ManufacturerID     *uuid.UUID `db:"manufacturer_id"     json:"manufacturer_id,omitempty"`
	StorageConditionID *uuid.UUID `db:"storage_condition_id" json:"storage_condition_id,omitempty"`

	// Presentation
	Strength      *string    `db:"strength"        json:"strength,omitempty"`  // display text, e.g. "500 mg"
	PackSize      *string    `db:"pack_size"       json:"pack_size,omitempty"` // e.g. "10x10"
	UnitsPerPack  *int       `db:"units_per_pack"  json:"units_per_pack,omitempty"`
	UnitsPerStrip *int       `db:"units_per_strip" json:"units_per_strip,omitempty"`
	PackageTypeID *uuid.UUID `db:"package_type_id" json:"package_type_id,omitempty"`
	SalesUnitID   *uuid.UUID `db:"sales_unit_id"   json:"sales_unit_id,omitempty"`

	// Regulatory
	RequiresPrescription bool    `db:"requires_prescription" json:"requires_prescription"`
	IsControlled         bool    `db:"is_controlled"         json:"is_controlled"`
	IsRefrigerated       bool    `db:"is_refrigerated"       json:"is_refrigerated"`
	IsImported           bool    `db:"is_imported"           json:"is_imported"`
	CountryOfOrigin      *string `db:"country_of_origin"     json:"country_of_origin,omitempty"`
	ImporterName         *string `db:"importer_name"         json:"importer_name,omitempty"`
	ImportLicenseNo      *string `db:"import_license_no"     json:"import_license_no,omitempty"`

	// Commercial
	MRP         *float64   `db:"mrp"             json:"mrp,omitempty"`          // denormalized copy; pricing owns price
	DefaultCost *float64   `db:"default_cost"    json:"default_cost,omitempty"` // denormalized copy; purchase/inventory own cost
	TaxRateID   *uuid.UUID `db:"tax_rate_id"     json:"tax_rate_id,omitempty"`
	HSNCode     *string    `db:"hsn_code"        json:"hsn_code,omitempty"`

	// Ops
	ShelfLifeMonths   *int    `db:"shelf_life_months" json:"shelf_life_months,omitempty"` // a default, not a batch expiry
	MinStockThreshold *int    `db:"min_stock_threshold" json:"min_stock_threshold,omitempty"`
	MaxStockThreshold *int    `db:"max_stock_threshold" json:"max_stock_threshold,omitempty"`
	ImageURL          *string `db:"image_url"           json:"image_url,omitempty"`

	IsActive bool `db:"is_active" json:"is_active"`
}

// ---------------------------------------------------------------------------
// medicine_variants
// ---------------------------------------------------------------------------

// MedicineVariant is a sellable and stockable package identity of a medicine.
//
// Table: medicine_variants (000006:52). A variant is a package identity, so
// "Napa 500 mg, 10 tablets" and "Napa 500 mg, 100 tablets" are two variants of
// one Medicine, each with its own SKU and barcode.
//
// Scope notes, per contract §5.2:
//   - Once a variant is referenced by purchase, inventory movement or sale, its
//     pack size and units-per-pack must not be mutated in place. A semantic
//     package change creates a new variant; a label-only correction is allowed
//     under audit.
//   - MRP is the catalog list price copy only. Branch selling price and price
//     history belong to pricing.
type MedicineVariant struct {
	common.BaseModel

	MedicineID   uuid.UUID `db:"medicine_id"  json:"medicine_id"`
	Name         string    `db:"name"         json:"name"`
	SKU          string    `db:"sku"          json:"sku"` // unique among non-deleted rows
	Barcode      *string   `db:"barcode"      json:"barcode,omitempty"`
	Strength     *string   `db:"strength"     json:"strength,omitempty"`
	PackSize     *string   `db:"pack_size"    json:"pack_size,omitempty"`
	UnitsPerPack *int      `db:"units_per_pack" json:"units_per_pack,omitempty"`
	MRP          *float64  `db:"mrp"          json:"mrp,omitempty"`

	IsActive bool `db:"is_active" json:"is_active"`
}

// ---------------------------------------------------------------------------
// medicine_barcodes
// ---------------------------------------------------------------------------

// MedicineBarcode is one identifier bound to a medicine and, optionally, to one
// of its variants.
//
// Table: medicine_barcodes (000006:72). A barcode is never silently
// transferred between variants, and a barcode that matches more than one
// active variant is an integrity error that must fail closed rather than be
// resolved automatically.
type MedicineBarcode struct {
	common.BaseModel

	MedicineID  uuid.UUID           `db:"medicine_id"  json:"medicine_id"`
	VariantID   *uuid.UUID          `db:"variant_id"   json:"variant_id,omitempty"`
	Barcode     string              `db:"barcode"      json:"barcode"`
	BarcodeType MedicineBarcodeType `db:"barcode_type" json:"barcode_type"`
	IsPrimary   bool                `db:"is_primary"   json:"is_primary"`
}

// ---------------------------------------------------------------------------
// medicine_price_history
// ---------------------------------------------------------------------------

// MedicinePriceHistory is one row of the pre-existing price history table.
//
// Table: medicine_price_history (000006:88). Medicine does not own price
// history: pricing owns the authoritative price books and history, and the
// Medicine price-history endpoint must delegate to an approved Pricing
// interface rather than serving this table directly (contract §1.12, §17).
// The type exists so the table can be read without inventing a second,
// competing history table.
type MedicinePriceHistory struct {
	common.BaseModel

	MedicineID    uuid.UUID         `db:"medicine_id"     json:"medicine_id"`
	PriceType     MedicinePriceType `db:"price_type"      json:"price_type"`
	OldPrice      *float64          `db:"old_price"       json:"old_price,omitempty"`
	NewPrice      float64           `db:"new_price"       json:"new_price"`
	ChangedBy     uuid.UUID         `db:"changed_by"      json:"changed_by"`
	EffectiveFrom time.Time         `db:"effective_from"  json:"effective_from"`
	Reason        *string           `db:"reason"          json:"reason,omitempty"`
}
