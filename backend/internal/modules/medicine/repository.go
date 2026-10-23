package medicine

// Medicine data access.
//
// The rules this file is built around:
//
//   - Every value is a bound parameter. Nothing that arrives in a request is
//     ever concatenated into SQL; the only identifiers that reach the statement
//     come from compile-time maps (the sort allow-list, the dictionary table
//     allow-list), so a client can never influence the shape of a query.
//
//   - Every statement is tenant-scoped in SQL. The medicines table has no
//     organization_id column, so the scope is resolved through
//     brands.organization_id inside the same statement. A row that is not in
//     the caller's organization is indistinguishable from a row that does not
//     exist: the repository never reports a foreign row and then filters it
//     out in Go.
//
//   - Reads go to the replica pool (ReadFromCtx) and writes go to the primary
//     (FromCtx), so a repository method works unchanged inside a transaction.
//
//   - Errors are returned raw for the service to translate, following the
//     brand module. The repository only adds sentinel errors for the cases the
//     contract names explicitly, so the service can map them onto stable API
//     codes instead of guessing from a driver error.
//
//   - Lists are keyset ("seek") paginated. The page boundary is the
//     (sort key, id) of the last row of the previous page, so the cost of a
//     page does not grow with the depth of the pagination, and concurrent
//     inserts cannot make a page skip or repeat rows the way OFFSET does. The
//     cursor is opaque and signed, and it is bound to the tenant and to the
//     filter set, so a cursor issued for one query is rejected by another.
//
// Two schema facts shape the SQL:
//
//   - sku is unique across the whole table, not per organization
//     (ux_medicines_sku, ux_medicine_variants_sku). A duplicate SKU in
//     another organization is therefore a unique violation, and the service
//     reports it as a conflict rather than as a not-found.
//
//   - medicines has a BEFORE INSERT OR UPDATE OF brand_id trigger
//     (tr_medicines_require_active_brand, migration 000024) that locks the
//     brand row and rejects a missing or inactive brand with SQLSTATE 23503
//     and CONSTRAINT medicines_brand_active_fkey. The service maps that
//     constraint to "the brand is not active"; every other foreign-key
//     violation means a reference row does not exist.
//
// mrp, default_cost, min_stock_threshold and max_stock_threshold are never
// written by this repository. Pricing owns price, purchase and inventory own
// acquisition cost, and inventory policy owns reorder thresholds (contract
// §2, §17); the Medicine module reads them and nothing else. The same holds
// for medicine_variants.mrp and for the whole medicine_price_history table,
// which the Medicine API must not serve until an approved Pricing interface
// exists to delegate to (contract §1.12).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend/internal/platform/db"
	"backend/pkg/crypto"
	"backend/pkg/pagination"
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrInvalidRequest marks a repository call that cannot be executed as
	// given: a missing tenant, an unsupported sort field, a missing limit or
	// a query that fails its own validation.
	ErrInvalidRequest = errors.New("medicine: invalid repository request")

	// ErrInvalidCursor marks a cursor that cannot be trusted: malformed,
	// tampered with, issued for another tenant or another filter set, or
	// issued for a different sort order.
	ErrInvalidCursor = errors.New("medicine: invalid cursor")

	// ErrAmbiguousIdentifier marks an exact lookup that matched more than one
	// active variant. POS has to fail closed rather than guess which product
	// the barcode means (contract §5.3).
	ErrAmbiguousIdentifier = errors.New("medicine: identifier matches more than one active variant")
)

// ---------------------------------------------------------------------------
// Column lists
//
// The lists mirror the models in model.go field for field, so a row can be
// scanned straight into the struct. The aliases match the ones the queries
// use, because RETURNING and ORDER BY both need to address the row.
// ---------------------------------------------------------------------------

const (
	medicineColumns = `m.id, m.created_at, m.updated_at, m.deleted_at,
		m.sku, m.barcode,
		m.name, m.display_name, m.description,
		m.generic_medicine_id, m.brand_id, m.category_id, m.dosage_form_id, m.route_id,
		m.manufacturer_id, m.storage_condition_id,
		m.strength, m.pack_size, m.units_per_pack, m.units_per_strip,
		m.package_type_id, m.sales_unit_id,
		m.requires_prescription, m.is_controlled, m.is_refrigerated, m.is_imported,
		m.country_of_origin, m.importer_name, m.import_license_no,
		m.mrp, m.default_cost, m.tax_rate_id, m.hsn_code,
		m.shelf_life_months, m.min_stock_threshold, m.max_stock_threshold, m.image_url,
		m.is_active`

	variantColumns = `v.id, v.created_at, v.updated_at, v.deleted_at,
		v.medicine_id, v.name, v.sku, v.barcode, v.strength, v.pack_size,
		v.units_per_pack, v.mrp, v.is_active`

	barcodeColumns = `bc.id, bc.created_at, bc.updated_at, bc.deleted_at,
		bc.medicine_id, bc.variant_id, bc.barcode, bc.barcode_type, bc.is_primary`
)

// ---------------------------------------------------------------------------
// Repository
// ---------------------------------------------------------------------------

// Repository is the Medicine data-access layer.
type Repository struct {
	db          *db.DB
	cursorCodec *CursorCodec
}

// NewRepository wires the repository to the shared database handle and the
// cursor codec.
//
// The cursor codec may be nil: every method that needs a cursor then fails
// with ErrInvalidRequest instead of silently degrading to offset pagination.
func NewRepository(database *db.DB, cursorCodec *CursorCodec) *Repository {
	return &Repository{db: database, cursorCodec: cursorCodec}
}

// rowScanner is the Scan subset shared by pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// ---------------------------------------------------------------------------
// Sorting
// ---------------------------------------------------------------------------

// medicineSortKind is the type of a sort key. The cursor has to carry the
// boundary value of the column it sorts on, so the kind decides how the value
// is encoded and how it is bound back as a query parameter.
type medicineSortKind int

const (
	medicineSortText medicineSortKind = iota
	medicineSortTime
)

// medicineSortColumn maps an API sort field to the expression it sorts on.
//
// The key set is identical to allowedMedicineSortFields in dto.go, which is
// the public allow-list; TestSortAllowListsMatch keeps the two in step.
//
// display_name is coalesced because the column is nullable. A row-wise
// comparison against NULL evaluates to NULL rather than to true, so a NULL
// display_name row would silently fall out of the keyset window.
var medicineSortColumn = map[string]struct {
	column string
	kind   medicineSortKind
}{
	"name":         {column: "m.name", kind: medicineSortText},
	"display_name": {column: "COALESCE(m.display_name, '')", kind: medicineSortText},
	"sku":          {column: "m.sku", kind: medicineSortText},
	"created_at":   {column: "m.created_at", kind: medicineSortTime},
	"updated_at":   {column: "m.updated_at", kind: medicineSortTime},
}

// defaultMedicineSortField is the sort used when the caller does not ask for
// one: the catalogue is browsed by name.
const defaultMedicineSortField = "name"

// medicineSortSpec resolves an API sort field into the expressions the
// keyset query needs.
type medicineSortSpec struct {
	field  string
	column string
	idExpr string
	kind   medicineSortKind
	desc   bool
}

// resolveMedicineSort validates a sort field against the public allow-list
// and resolves it into a keyset specification.
//
// Only the public allow-list is consulted, never the column map alone: the
// allow-list is the API contract, the column map is an implementation detail
// of this repository.
func resolveMedicineSort(fields []pagination.SortField) (medicineSortSpec, error) {
	spec := medicineSortSpec{
		field:  defaultMedicineSortField,
		column: medicineSortColumn[defaultMedicineSortField].column,
		idExpr: "m.id",
		kind:   medicineSortColumn[defaultMedicineSortField].kind,
	}
	if len(fields) == 0 {
		return spec, nil
	}
	if len(fields) > 1 {
		// A keyset cursor carries a single boundary value, so it can only
		// describe one sort column. Multi-column ordering stays available
		// through the offset list.
		return medicineSortSpec{}, fmt.Errorf("%w: cursor pagination supports exactly one sort field", ErrInvalidRequest)
	}

	field := fields[0].Field
	if _, allowed := allowedMedicineSortFields[field]; !allowed {
		return medicineSortSpec{}, fmt.Errorf("%w: unsupported sort field %q", ErrInvalidRequest, field)
	}
	column, ok := medicineSortColumn[field]
	if !ok {
		return medicineSortSpec{}, fmt.Errorf("%w: unsupported sort field %q", ErrInvalidRequest, field)
	}
	spec.field = field
	spec.column = column.column
	spec.kind = column.kind
	spec.desc = fields[0].Desc
	return spec, nil
}

// orderBy renders the ORDER BY clause of a keyset query, always with the id
// tie-breaker so the order is total and the page boundary is unambiguous.
func (s medicineSortSpec) orderBy() string {
	direction := "ASC"
	if s.desc {
		direction = "DESC"
	}
	return s.column + " " + direction + ", " + s.idExpr + " " + direction
}

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

// cursorVersion is the payload version. Bumping it invalidates every
// outstanding cursor, which is what a change to the payload shape requires.
const cursorVersion = 1

// cursorPayload is the decoded content of a Medicine cursor.
//
// It carries the boundary of the previous page (the value of the sort column
// and the id tie-breaker) and the binding that says which tenant and which
// filter set the cursor was issued for.
type cursorPayload struct {
	Version int    `json:"v"`
	Binding string `json:"b"`
	Field   string `json:"f"`
	Desc    bool   `json:"d"`
	Key     string `json:"k"`
	ID      string `json:"i"`
}

// CursorCodec signs and verifies the opaque cursors of the Medicine module.
//
// The token is base64url(payload) + "." + HMAC-SHA256(payload), so it is safe
// in a query string and cannot be edited by a client: a forged cursor fails
// the signature check and is indistinguishable from a malformed one.
type CursorCodec struct {
	key []byte
}

// NewCursorCodec builds a codec from a signing key. The key must be at least
// 32 bytes and must be the stable application secret
// (pagination.cursor_signing_key): every instance has to sign and verify with
// the same key, and rotating it invalidates the outstanding cursors.
func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("medicine: cursor signing key must be at least 32 bytes (got %d)", len(key))
	}
	return &CursorCodec{key: append([]byte(nil), key...)}, nil
}

// Encode signs a payload into an opaque token.
func (c *CursorCodec) Encode(payload cursorPayload) (string, error) {
	if c == nil {
		return "", fmt.Errorf("%w: cursor signing is not configured", ErrInvalidRequest)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("medicine: marshal cursor payload: %w", err)
	}
	return crypto.SignToken(c.key, body), nil
}

// Decode verifies a token and returns its payload.
func (c *CursorCodec) Decode(token string) (cursorPayload, error) {
	if c == nil {
		return cursorPayload{}, fmt.Errorf("%w: cursor signing is not configured", ErrInvalidRequest)
	}
	raw, ok := crypto.VerifyToken(c.key, token)
	if !ok {
		return cursorPayload{}, fmt.Errorf("%w: the cursor is malformed or was tampered with", ErrInvalidCursor)
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: the cursor payload is malformed", ErrInvalidCursor)
	}
	if payload.Version != cursorVersion {
		return cursorPayload{}, fmt.Errorf("%w: unsupported cursor version", ErrInvalidCursor)
	}
	if payload.Field == "" || payload.ID == "" {
		return cursorPayload{}, fmt.Errorf("%w: the cursor payload is incomplete", ErrInvalidCursor)
	}
	return payload, nil
}

// medicineCursorBinding hashes the tenant scope and the filter set of a
// query.
//
// Binding the cursor to the filters is what stops a client from stitching
// pages of two different result sets together: a cursor issued for one filter
// set simply does not validate against another.
func medicineCursorBinding(orgID uuid.UUID, q *ListMedicinesQuery, field string, desc bool) string {
	type canonical struct {
		Org                  uuid.UUID  `json:"org"`
		CategoryID           *uuid.UUID `json:"category,omitempty"`
		BrandID              *uuid.UUID `json:"brand,omitempty"`
		GenericID            *uuid.UUID `json:"generic,omitempty"`
		IsActive             *bool      `json:"active,omitempty"`
		RequiresPrescription *bool      `json:"rx,omitempty"`
		IsControlled         *bool      `json:"controlled,omitempty"`
		Q                    string     `json:"q,omitempty"`
		Field                string     `json:"field"`
		Desc                 bool       `json:"desc"`
	}
	body, err := json.Marshal(canonical{
		Org:                  orgID,
		CategoryID:           q.CategoryUUID(),
		BrandID:              q.BrandUUID(),
		GenericID:            q.GenericUUID(),
		IsActive:             q.IsActive,
		RequiresPrescription: q.RequiresPrescription,
		IsControlled:         q.IsControlled,
		Q:                    q.NormalizedQ(),
		Field:                field,
		Desc:                 desc,
	})
	if err != nil {
		// The canonical form contains only JSON-safe values, so this cannot
		// happen; fall back to a value that can never match a binding.
		return "unencodable"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// cursorKeyValue renders the boundary value of a sort column for the cursor
// payload.
func cursorKeyValue(kind medicineSortKind, value any) (string, error) {
	switch kind {
	case medicineSortTime:
		t, ok := value.(time.Time)
		if !ok {
			return "", fmt.Errorf("medicine: unexpected sort key type %T", value)
		}
		return t.UTC().Format(time.RFC3339Nano), nil
	default:
		s, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("medicine: unexpected sort key type %T", value)
		}
		return s, nil
	}
}

// cursorKeyParam converts a cursor boundary value back into the parameter
// that the keyset predicate binds.
func cursorKeyParam(kind medicineSortKind, value string) (any, error) {
	if kind != medicineSortTime {
		return value, nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("%w: the cursor timestamp is malformed", ErrInvalidCursor)
	}
	return t.UTC(), nil
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

// scanMedicine maps one row into a Medicine.
//
// The nullable columns are scanned through a pointer to a pointer on purpose:
// pgx allocates the inner pointer for a value and leaves it nil for SQL NULL,
// so an empty string and a NULL stay distinguishable.
func scanMedicine(row rowScanner) (*Medicine, error) {
	var v Medicine
	if err := row.Scan(
		&v.ID, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
		&v.SKU, &v.Barcode,
		&v.Name, &v.DisplayName, &v.Description,
		&v.GenericMedicineID, &v.BrandID, &v.CategoryID, &v.DosageFormID, &v.RouteID,
		&v.ManufacturerID, &v.StorageConditionID,
		&v.Strength, &v.PackSize, &v.UnitsPerPack, &v.UnitsPerStrip,
		&v.PackageTypeID, &v.SalesUnitID,
		&v.RequiresPrescription, &v.IsControlled, &v.IsRefrigerated, &v.IsImported,
		&v.CountryOfOrigin, &v.ImporterName, &v.ImportLicenseNo,
		&v.MRP, &v.DefaultCost, &v.TaxRateID, &v.HSNCode,
		&v.ShelfLifeMonths, &v.MinStockThreshold, &v.MaxStockThreshold, &v.ImageURL,
		&v.IsActive,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// scanMedicineWithKey maps one row into a Medicine and captures the value of
// the sort column, which the cursor needs as the next page boundary.
func scanMedicineWithKey(row rowScanner, key *any) (*Medicine, error) {
	var v Medicine
	if err := row.Scan(
		&v.ID, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
		&v.SKU, &v.Barcode,
		&v.Name, &v.DisplayName, &v.Description,
		&v.GenericMedicineID, &v.BrandID, &v.CategoryID, &v.DosageFormID, &v.RouteID,
		&v.ManufacturerID, &v.StorageConditionID,
		&v.Strength, &v.PackSize, &v.UnitsPerPack, &v.UnitsPerStrip,
		&v.PackageTypeID, &v.SalesUnitID,
		&v.RequiresPrescription, &v.IsControlled, &v.IsRefrigerated, &v.IsImported,
		&v.CountryOfOrigin, &v.ImporterName, &v.ImportLicenseNo,
		&v.MRP, &v.DefaultCost, &v.TaxRateID, &v.HSNCode,
		&v.ShelfLifeMonths, &v.MinStockThreshold, &v.MaxStockThreshold, &v.ImageURL,
		&v.IsActive,
		key,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// scanVariant maps one row into a MedicineVariant.
func scanVariant(row rowScanner) (*MedicineVariant, error) {
	var v MedicineVariant
	if err := row.Scan(
		&v.ID, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
		&v.MedicineID, &v.Name, &v.SKU, &v.Barcode, &v.Strength, &v.PackSize,
		&v.UnitsPerPack, &v.MRP, &v.IsActive,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// scanBarcode maps one row into a MedicineBarcode.
func scanBarcode(row rowScanner) (*MedicineBarcode, error) {
	var v MedicineBarcode
	if err := row.Scan(
		&v.ID, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
		&v.MedicineID, &v.VariantID, &v.Barcode, &v.BarcodeType, &v.IsPrimary,
	); err != nil {
		return nil, err
	}
	return &v, nil
}

// ---------------------------------------------------------------------------
// medicines — writes
// ---------------------------------------------------------------------------

// Create inserts a catalogue entry and returns the stored row.
//
// The insert is an INSERT ... SELECT over the brand, so the tenant check is
// part of the statement: a brand that belongs to another organization, or one
// that has been deleted, produces no row at all, and the caller gets
// db.ErrNoRows instead of a hint that the brand exists.
//
// An inactive brand is rejected by the tr_medicines_require_active_brand
// trigger with SQLSTATE 23503 and CONSTRAINT medicines_brand_active_fkey;
// every other foreign-key violation means a referenced dictionary row does
// not exist.
//
// mrp, default_cost and the stock thresholds are not written: pricing,
// purchase and inventory own them (contract §17).
func (r *Repository) Create(ctx context.Context, orgID uuid.UUID, m *Medicine) (*Medicine, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%w: medicine is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	columns := []string{
		"sku", "barcode", "name", "display_name", "description",
		"generic_medicine_id", "brand_id", "category_id", "dosage_form_id", "route_id",
		"manufacturer_id", "storage_condition_id", "strength", "pack_size", "units_per_pack",
		"units_per_strip", "package_type_id", "sales_unit_id", "requires_prescription",
		"is_controlled", "is_refrigerated", "is_imported", "country_of_origin",
		"importer_name", "import_license_no", "tax_rate_id", "hsn_code",
		"shelf_life_months", "image_url", "is_active",
	}
	values := []any{
		m.SKU, m.Barcode, m.Name, m.DisplayName, m.Description,
		m.GenericMedicineID, m.BrandID, m.CategoryID, m.DosageFormID, m.RouteID,
		m.ManufacturerID, m.StorageConditionID, m.Strength, m.PackSize, m.UnitsPerPack,
		m.UnitsPerStrip, m.PackageTypeID, m.SalesUnitID, m.RequiresPrescription,
		m.IsControlled, m.IsRefrigerated, m.IsImported, m.CountryOfOrigin,
		m.ImporterName, m.ImportLicenseNo, m.TaxRateID, m.HSNCode,
		m.ShelfLifeMonths, m.ImageURL, m.IsActive,
	}
	brandPlaceholder, err := placeholderOf(columns, "brand_id", 1)
	if err != nil {
		return nil, err
	}

	q := `INSERT INTO medicines AS m (` + strings.Join(columns, ", ") + `)
	SELECT ` + numberedPlaceholders(1, len(values)) + `
	FROM brands b
	WHERE b.id = ` + brandPlaceholder + `
	  AND b.organization_id = $` + strconv.Itoa(len(values)+1) + `
	  AND b.deleted_at IS NULL
	RETURNING ` + medicineColumns

	created, err := scanMedicine(r.db.FromCtx(ctx).QueryRow(ctx, q, append(values, orgID)...))
	if err != nil {
		return nil, fmt.Errorf("medicine create: %w", err)
	}
	return created, nil
}

// Update writes the mutable columns of a medicine and returns the refreshed
// row.
//
// The write is tenant-scoped through the brand, so a row outside the caller's
// organization is not updated and reads as missing.
//
// sku and brand_id are not written: the SKU is the product's exact-lookup key
// and the brand carries the tenant scope, and rewriting either of them
// reinterprets what the historical records referred to (contract §5.3, §10).
// mrp, default_cost and the stock thresholds belong to other modules.
func (r *Repository) Update(ctx context.Context, orgID uuid.UUID, m *Medicine) (*Medicine, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%w: medicine is required", ErrInvalidRequest)
	}
	if m.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	setClause, setValues := buildSetClause(3, []sqlAssignment{
		{"barcode", m.Barcode},
		{"name", m.Name},
		{"display_name", m.DisplayName},
		{"description", m.Description},
		{"generic_medicine_id", m.GenericMedicineID},
		{"category_id", m.CategoryID},
		{"dosage_form_id", m.DosageFormID},
		{"route_id", m.RouteID},
		{"manufacturer_id", m.ManufacturerID},
		{"storage_condition_id", m.StorageConditionID},
		{"strength", m.Strength},
		{"pack_size", m.PackSize},
		{"units_per_pack", m.UnitsPerPack},
		{"units_per_strip", m.UnitsPerStrip},
		{"package_type_id", m.PackageTypeID},
		{"sales_unit_id", m.SalesUnitID},
		{"requires_prescription", m.RequiresPrescription},
		{"is_controlled", m.IsControlled},
		{"is_refrigerated", m.IsRefrigerated},
		{"is_imported", m.IsImported},
		{"country_of_origin", m.CountryOfOrigin},
		{"importer_name", m.ImporterName},
		{"import_license_no", m.ImportLicenseNo},
		{"tax_rate_id", m.TaxRateID},
		{"hsn_code", m.HSNCode},
		{"shelf_life_months", m.ShelfLifeMonths},
		{"image_url", m.ImageURL},
		{"is_active", m.IsActive},
	})

	q := `UPDATE medicines AS m SET ` + setClause + `
	WHERE m.id = $1
	  AND m.deleted_at IS NULL
	  AND EXISTS (SELECT 1 FROM brands b WHERE b.id = m.brand_id AND b.organization_id = $2)
	RETURNING ` + medicineColumns

	updated, err := scanMedicine(r.db.FromCtx(ctx).QueryRow(ctx, q,
		append([]any{m.ID, orgID}, setValues...)...,
	))
	if err != nil {
		return nil, fmt.Errorf("medicine update: %w", err)
	}
	return updated, nil
}

// SetActive activates or deactivates a catalogue entry and reports whether a
// row was affected.
//
// Deactivation is preferred over deletion: history keeps its references
// (contract §7).
func (r *Repository) SetActive(ctx context.Context, orgID, id uuid.UUID, active bool) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if id == uuid.Nil {
		return false, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return false, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `UPDATE medicines AS m SET is_active = $3
	WHERE m.id = $1
	  AND m.deleted_at IS NULL
	  AND EXISTS (SELECT 1 FROM brands b WHERE b.id = m.brand_id AND b.organization_id = $2)`

	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID, active)
	if err != nil {
		return false, fmt.Errorf("medicine set active: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SoftDelete marks a medicine deleted and reports whether a row was affected.
//
// The row is only marked, never removed: inventory batches, stock movements,
// purchases and sales reference it (contract §10). The service decides
// whether a referenced product may be deleted at all — see
// MedicineIsReferenced.
func (r *Repository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if id == uuid.Nil {
		return false, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return false, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `UPDATE medicines AS m SET deleted_at = now()
	WHERE m.id = $1
	  AND m.deleted_at IS NULL
	  AND EXISTS (SELECT 1 FROM brands b WHERE b.id = m.brand_id AND b.organization_id = $2)`

	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, id, orgID)
	if err != nil {
		return false, fmt.Errorf("medicine soft delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// medicines — reads
// ---------------------------------------------------------------------------

// FindByID returns a live medicine of the caller's organization, or
// db.ErrNoRows.
//
// A medicine of another organization is reported as missing rather than as
// forbidden: the response must not reveal that the row exists.
func (r *Repository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*Medicine, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + medicineColumns + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	WHERE m.id = $1 AND m.deleted_at IS NULL AND b.organization_id = $2`

	found, err := scanMedicine(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine find by id: %w", err)
	}
	return found, nil
}

// FindByIDForUpdate is FindByID with a row lock, for read-modify-write.
//
// Only the medicine row is locked: the brand is joined for the tenant check
// and locking it as well would serialize every product of the brand behind
// one row.
func (r *Repository) FindByIDForUpdate(ctx context.Context, orgID, id uuid.UUID) (*Medicine, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + medicineColumns + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	WHERE m.id = $1 AND m.deleted_at IS NULL AND b.organization_id = $2
	FOR UPDATE OF m`

	found, err := scanMedicine(r.db.FromCtx(ctx).QueryRow(ctx, q, id, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine find by id for update: %w", err)
	}
	return found, nil
}

// FindBySKU returns the medicine with the given catalogue SKU, or
// db.ErrNoRows.
//
// The SKU is unique across the whole table, so a SKU that belongs to another
// organization is reported as missing here and as a unique violation when it
// is written.
func (r *Repository) FindBySKU(ctx context.Context, orgID uuid.UUID, sku string) (*Medicine, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(sku) == "" {
		return nil, fmt.Errorf("%w: sku is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + medicineColumns + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	WHERE m.sku = $1 AND m.deleted_at IS NULL AND b.organization_id = $2`

	found, err := scanMedicine(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, sku, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine find by sku: %w", err)
	}
	return found, nil
}

// FindByBarcode returns the medicine with the given medicine-level barcode,
// or db.ErrNoRows.
func (r *Repository) FindByBarcode(ctx context.Context, orgID uuid.UUID, barcode string) (*Medicine, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(barcode) == "" {
		return nil, fmt.Errorf("%w: barcode is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + medicineColumns + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	WHERE m.barcode = $1 AND m.deleted_at IS NULL AND b.organization_id = $2`

	found, err := scanMedicine(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, barcode, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine find by barcode: %w", err)
	}
	return found, nil
}

// MedicineIsReferenced reports whether any operational record still points at
// the medicine.
//
// The service uses it to decide between a deactivation and a deletion
// (contract §10). Every arm is an indexed lookup on the referencing column.
func (r *Repository) MedicineIsReferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if id == uuid.Nil {
		return false, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}

	const q = `SELECT
		EXISTS (SELECT 1 FROM inventory_batches  WHERE medicine_id = $1)
		OR EXISTS (SELECT 1 FROM stock_movements  WHERE medicine_id = $1)
		OR EXISTS (SELECT 1 FROM purchase_items   WHERE medicine_id = $1)
		OR EXISTS (SELECT 1 FROM sale_items      WHERE medicine_id = $1)
		OR EXISTS (SELECT 1 FROM sales_return_items WHERE medicine_id = $1)`

	var referenced bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id).Scan(&referenced); err != nil {
		return false, fmt.Errorf("medicine reference check: %w", err)
	}
	return referenced, nil
}

// ---------------------------------------------------------------------------
// medicines — list
// ---------------------------------------------------------------------------

// MedicinePage is one page of catalogue entries.
type MedicinePage struct {
	Items      []Medicine
	NextCursor string
	HasMore    bool
}

// List returns one offset page of the catalogue together with the total
// number of matching rows.
//
// The offset page is the right shape for an administration screen that shows
// a count and lets the user jump to a page. The cursor page (ListCursor) is
// the right shape for a long list: its cost does not grow with the depth.
//
// The query is tenant-scoped through the brand join, and the free-text term
// is matched with ILIKE against the four indexed columns (name,
// display_name, sku and barcode), so PostgreSQL can serve it from the
// trigram indexes instead of scanning the table.
func (r *Repository) List(
	ctx context.Context,
	orgID uuid.UUID,
	query ListMedicinesQuery,
	sort []pagination.SortField,
) ([]Medicine, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}
	if orgID == uuid.Nil {
		return nil, 0, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return nil, 0, errors.Join(validation...)
	}

	where, args := buildMedicineFilters(&query, orgID)

	var total int64
	countSQL := `SELECT count(*)
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id` + where
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("medicine count: %w", err)
	}

	orderBy, err := buildMedicineOrderBy(sort)
	if err != nil {
		return nil, 0, err
	}

	limit := query.Limit
	offset := query.OffsetSQL()
	pageArgs := append(append([]any{}, args...), limit, offset)

	listSQL := `SELECT ` + medicineColumns + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id` + where + `
	ORDER BY ` + orderBy + `
	LIMIT $` + strconv.Itoa(len(pageArgs)-1) + ` OFFSET $` + strconv.Itoa(len(pageArgs))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("medicine list query: %w", err)
	}
	defer rows.Close()

	items := make([]Medicine, 0, limit)
	for rows.Next() {
		item, err := scanMedicine(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan medicine: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate medicine list: %w", err)
	}
	return items, total, nil
}

// ListCursor returns one keyset page of the catalogue.
//
// The page boundary is the (sort key, id) of the last row of the previous
// page, so the database walks an index instead of counting rows it throws
// away, and rows that are inserted while the client pages cannot make a page
// skip or repeat rows.
//
// The cursor is opaque, signed and bound to the tenant and to the filter set:
// a cursor issued for one query is rejected by another, so pages of two
// different result sets cannot be stitched together.
//
// The sort key of the last row is selected alongside the row, because the
// cursor has to carry it back to the client.
func (r *Repository) ListCursor(
	ctx context.Context,
	orgID uuid.UUID,
	query ListMedicinesQuery,
	sort []pagination.SortField,
	cursor string,
) (MedicinePage, error) {
	if err := r.ready(); err != nil {
		return MedicinePage{}, err
	}
	if orgID == uuid.Nil {
		return MedicinePage{}, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if r.cursorCodec == nil {
		return MedicinePage{}, fmt.Errorf("%w: cursor signing is not configured", ErrInvalidRequest)
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return MedicinePage{}, errors.Join(validation...)
	}

	spec, err := resolveMedicineSort(sort)
	if err != nil {
		return MedicinePage{}, err
	}

	binding := medicineCursorBinding(orgID, &query, spec.field, spec.desc)

	var payload *cursorPayload
	if cursor != "" {
		decoded, decodeErr := r.cursorCodec.Decode(cursor)
		if decodeErr != nil {
			return MedicinePage{}, decodeErr
		}
		if decoded.Binding != binding {
			return MedicinePage{}, fmt.Errorf("%w: the cursor was issued for a different query", ErrInvalidCursor)
		}
		if decoded.Field != spec.field || decoded.Desc != spec.desc {
			return MedicinePage{}, fmt.Errorf("%w: the cursor was issued for a different sort order", ErrInvalidCursor)
		}
		payload = &decoded
	}

	where, args := buildMedicineFilters(&query, orgID)

	if payload != nil {
		key, keyErr := cursorKeyParam(spec.kind, payload.Key)
		if keyErr != nil {
			return MedicinePage{}, keyErr
		}
		id, idErr := uuid.Parse(payload.ID)
		if idErr != nil {
			return MedicinePage{}, fmt.Errorf("%w: the cursor id is malformed", ErrInvalidCursor)
		}
		args = append(args, key, id)
		operator := ">"
		if spec.desc {
			operator = "<"
		}
		where += fmt.Sprintf(
			" AND (%s, %s) %s ($%d, $%d)",
			spec.column, spec.idExpr, operator, len(args)-1, len(args),
		)
	}

	limit := query.Limit
	pageArgs := append(append([]any{}, args...), limit+1)

	listSQL := `SELECT ` + medicineColumns + `, ` + spec.column + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id` + where + `
	ORDER BY ` + spec.orderBy() + `
	LIMIT $` + strconv.Itoa(len(pageArgs))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, pageArgs...)
	if err != nil {
		return MedicinePage{}, fmt.Errorf("medicine cursor query: %w", err)
	}
	defer rows.Close()

	items := make([]Medicine, 0, limit)
	hasMore := false
	var lastKey any
	var lastID uuid.UUID
	for rows.Next() {
		var key any
		item, err := scanMedicineWithKey(rows, &key)
		if err != nil {
			return MedicinePage{}, fmt.Errorf("scan medicine: %w", err)
		}
		if len(items) == limit {
			// The extra row proves another page exists.
			hasMore = true
			break
		}
		items = append(items, *item)
		lastKey = key
		lastID = item.ID
	}
	if err := rows.Err(); err != nil {
		return MedicinePage{}, fmt.Errorf("iterate medicine list: %w", err)
	}

	page := MedicinePage{Items: items, HasMore: hasMore}
	if !hasMore || len(items) == 0 {
		return page, nil
	}

	keyValue, err := cursorKeyValue(spec.kind, lastKey)
	if err != nil {
		return MedicinePage{}, err
	}
	next, err := r.cursorCodec.Encode(cursorPayload{
		Version: cursorVersion,
		Binding: binding,
		Field:   spec.field,
		Desc:    spec.desc,
		Key:     keyValue,
		ID:      lastID.String(),
	})
	if err != nil {
		return MedicinePage{}, err
	}
	page.NextCursor = next
	return page, nil
}

// Search returns one keyset page of the catalogue for a free-text term.
//
// The term is mandatory: matching everything is the list endpoint's job, and
// an unbounded term is a cheap way to force an expensive trigram scan
// (contract §1.9, §8.2).
// Search returns the catalogue entries that match a free-text term.
//
// The term is matched with ILIKE against the four indexed columns (name,
// display_name, sku and barcode), so PostgreSQL can serve it from the trigram
// indexes.
//
// The result set is keyset paginated: a search is unbounded by nature, so an
// offset page would make the database count rows it throws away.
func (r *Repository) Search(
	ctx context.Context,
	orgID uuid.UUID,
	query SearchMedicinesQuery,
	sort []pagination.SortField,
	cursor string,
) (MedicinePage, error) {
	if err := r.ready(); err != nil {
		return MedicinePage{}, err
	}
	if validation := query.NormalizeAndValidate(); len(validation) > 0 {
		return MedicinePage{}, errors.Join(validation...)
	}
	if query.NormalizedQ() == "" {
		return MedicinePage{}, fmt.Errorf("%w: a search term is required", ErrInvalidRequest)
	}

	term := query.NormalizedQ()
	listQuery := ListMedicinesQuery{
		Offset: query.Offset,
		Q:      &term,
	}
	return r.ListCursor(ctx, orgID, listQuery, sort, cursor)
}

// ---------------------------------------------------------------------------
// medicines — reference labels
// ---------------------------------------------------------------------------

// LoadReferenceLabels batch-loads the joined dictionary labels of the given
// medicines in a single round trip.
//
// The list endpoint shows the brand, the category and the clinical
// references of every row. Loading them one row at a time would be an N+1
// query per page, so they are fetched in one statement and keyed by medicine
// id (contract §9, "Performance").
//
// Medicines that are missing or outside the caller's organization are simply
// absent from the result.
func (r *Repository) LoadReferenceLabels(
	ctx context.Context,
	orgID uuid.UUID,
	ids []uuid.UUID,
) (map[uuid.UUID]MedicineReferenceLabels, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	labels := map[uuid.UUID]MedicineReferenceLabels{}
	if len(ids) == 0 {
		return labels, nil
	}

	unique := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return labels, nil
	}

	const q = `SELECT
		m.id, b.name, pc.name,
		gm.name, df.name, rt.name, mf.name, sc.name, pt.name, ut.name, tr.code
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	JOIN product_categories pc ON pc.id = m.category_id
	LEFT JOIN generic_medicines gm ON gm.id = m.generic_medicine_id
	LEFT JOIN dosage_forms df ON df.id = m.dosage_form_id
	LEFT JOIN routes rt ON rt.id = m.route_id
	LEFT JOIN manufacturers mf ON mf.id = m.manufacturer_id
	LEFT JOIN storage_conditions sc ON sc.id = m.storage_condition_id
	LEFT JOIN package_types pt ON pt.id = m.package_type_id
	LEFT JOIN unit_types ut ON ut.id = m.sales_unit_id
	LEFT JOIN tax_rates tr ON tr.id = m.tax_rate_id
	WHERE m.id = ANY($1) AND m.deleted_at IS NULL AND b.organization_id = $2`

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, unique, orgID)
	if err != nil {
		return nil, fmt.Errorf("medicine reference labels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var label MedicineReferenceLabels
		if err := rows.Scan(
			&id,
			&label.BrandName, &label.CategoryName,
			&label.GenericName, &label.DosageFormName, &label.RouteName,
			&label.ManufacturerName, &label.StorageConditionName,
			&label.PackageTypeName, &label.SalesUnitName, &label.TaxRateCode,
		); err != nil {
			return nil, fmt.Errorf("scan medicine reference labels: %w", err)
		}
		labels[id] = label
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate medicine reference labels: %w", err)
	}
	return labels, nil
}

// ---------------------------------------------------------------------------
// Reference validation
// ---------------------------------------------------------------------------

// dictionaryTables maps a public reference name to its dictionary table.
//
// The values are compile-time constants, never request input, so the table
// name can never be influenced from outside the module. The dictionary tables
// are global: they carry no organization_id, so they are shared by every
// tenant and only have to exist and be active.
var dictionaryTables = map[string]string{
	"generic_medicines":  "generic_medicines",
	"product_categories": "product_categories",
	"dosage_forms":       "dosage_forms",
	"routes":             "routes",
	"storage_conditions": "storage_conditions",
	"package_types":      "package_types",
	"unit_types":         "unit_types",
	"tax_rates":          "tax_rates",
}

// ReferenceIsUsable reports whether a dictionary row exists, is not deleted
// and is active.
//
// The table is selected from the compile-time allow-list; an unknown name is
// an error rather than an empty result, so a typo cannot silently disable a
// reference check.
func (r *Repository) ReferenceIsUsable(ctx context.Context, table string, id uuid.UUID) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if id == uuid.Nil {
		return false, fmt.Errorf("%w: reference id is required", ErrInvalidRequest)
	}
	name, ok := dictionaryTables[table]
	if !ok {
		return false, fmt.Errorf("%w: unknown reference table %q", ErrInvalidRequest, table)
	}

	q := `SELECT EXISTS (
		SELECT 1 FROM ` + name + `
		WHERE id = $1 AND deleted_at IS NULL AND is_active = TRUE
	)`
	var usable bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id).Scan(&usable); err != nil {
		return false, fmt.Errorf("medicine reference check on %s: %w", name, err)
	}
	return usable, nil
}

// BrandIsUsable reports whether a brand exists, is not deleted, is active and
// belongs to the caller's organization.
//
// A brand is tenant-owned (brands.organization_id, migration 000024), so the
// check is organization-scoped: a brand of another organization is reported
// as unusable rather than as missing.
func (r *Repository) BrandIsUsable(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if orgID == uuid.Nil {
		return false, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if id == uuid.Nil {
		return false, fmt.Errorf("%w: brand id is required", ErrInvalidRequest)
	}

	const q = `SELECT EXISTS (
		SELECT 1 FROM brands
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND is_active = TRUE
	)`
	var usable bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID).Scan(&usable); err != nil {
		return false, fmt.Errorf("medicine brand check: %w", err)
	}
	return usable, nil
}

// ManufacturerIsUsable reports whether a manufacturer exists, is not deleted,
// is active and belongs to the caller's organization.
func (r *Repository) ManufacturerIsUsable(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if orgID == uuid.Nil {
		return false, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if id == uuid.Nil {
		return false, fmt.Errorf("%w: manufacturer id is required", ErrInvalidRequest)
	}

	const q = `SELECT EXISTS (
		SELECT 1 FROM manufacturers
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND is_active = TRUE
	)`
	var usable bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, id, orgID).Scan(&usable); err != nil {
		return false, fmt.Errorf("medicine manufacturer check: %w", err)
	}
	return usable, nil
}

// BrandManufacturerID returns the manufacturer the brand references.
//
// The medicine carries a denormalized manufacturer_id while the brand already
// references one (contract §17). The service compares the two so a product
// cannot claim a manufacturer that contradicts its brand.
//
// A brand of another organization is reported as "no brand" (a nil
// identifier), not as an error: the caller must not learn about rows outside
// its own organization.
func (r *Repository) BrandManufacturerID(ctx context.Context, orgID, brandID uuid.UUID) (*uuid.UUID, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if brandID == uuid.Nil {
		return nil, fmt.Errorf("%w: brand id is required", ErrInvalidRequest)
	}

	const q = `SELECT manufacturer_id FROM brands
	WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	var manufacturerID *uuid.UUID
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, brandID, orgID).Scan(&manufacturerID); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("medicine brand manufacturer lookup: %w", err)
	}
	return manufacturerID, nil
}

// ---------------------------------------------------------------------------
// medicine_variants
// ---------------------------------------------------------------------------

// VariantPage is one page of package variants.
type VariantPage struct {
	Items      []MedicineVariant
	NextCursor string
	HasMore    bool
}

// CreateVariant inserts a package variant and returns the stored row.
//
// The insert is scoped to the medicine and to the caller's organization in
// the statement itself, so a variant can never be attached to a product of
// another tenant.
//
// mrp is not written: pricing owns the catalogue list price (contract §17).
func (r *Repository) CreateVariant(
	ctx context.Context,
	orgID, medicineID uuid.UUID,
	v *MedicineVariant,
) (*MedicineVariant, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("%w: variant is required", ErrInvalidRequest)
	}
	if medicineID == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	columns := []string{
		"medicine_id", "name", "sku", "barcode", "strength", "pack_size",
		"units_per_pack", "is_active",
	}
	values := []any{
		medicineID, v.Name, v.SKU, v.Barcode, v.Strength, v.PackSize,
		v.UnitsPerPack, v.IsActive,
	}

	q := `INSERT INTO medicine_variants AS v (` + strings.Join(columns, ", ") + `)
	SELECT ` + numberedPlaceholders(1, len(values)) + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	WHERE m.id = $1 AND m.deleted_at IS NULL AND b.organization_id = $` + strconv.Itoa(len(values)+1) + `
	RETURNING ` + variantColumns

	created, err := scanVariant(r.db.FromCtx(ctx).QueryRow(ctx, q, append(values, orgID)...))
	if err != nil {
		return nil, fmt.Errorf("medicine variant create: %w", err)
	}
	return created, nil
}

// FindVariant returns a live variant of a live medicine of the caller's
// organization, or db.ErrNoRows.
func (r *Repository) FindVariant(
	ctx context.Context,
	orgID, medicineID, variantID uuid.UUID,
) (*MedicineVariant, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if medicineID == uuid.Nil || variantID == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id and variant id are required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + variantColumns + `
	FROM medicine_variants v
	JOIN medicines m ON m.id = v.medicine_id
	JOIN brands b ON b.id = m.brand_id
	WHERE v.id = $1
	  AND v.medicine_id = $2
	  AND v.deleted_at IS NULL
	  AND m.deleted_at IS NULL
	  AND b.organization_id = $3`

	found, err := scanVariant(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, variantID, medicineID, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine variant find: %w", err)
	}
	return found, nil
}

// FindVariantForUpdate is FindVariant with a row lock, for the
// read-modify-write that protects a conversion change.
func (r *Repository) FindVariantForUpdate(
	ctx context.Context,
	orgID, medicineID, variantID uuid.UUID,
) (*MedicineVariant, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if medicineID == uuid.Nil || variantID == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id and variant id are required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + variantColumns + `
	FROM medicine_variants v
	JOIN medicines m ON m.id = v.medicine_id
	JOIN brands b ON b.id = m.brand_id
	WHERE v.id = $1
	  AND v.medicine_id = $2
	  AND v.deleted_at IS NULL
	  AND m.deleted_at IS NULL
	  AND b.organization_id = $3
	FOR UPDATE OF v`

	found, err := scanVariant(r.db.FromCtx(ctx).QueryRow(ctx, q, variantID, medicineID, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine variant find for update: %w", err)
	}
	return found, nil
}

// FindVariantBySKU returns the variant with the given SKU, or db.ErrNoRows.
//
// The variant SKU is unique across the whole table, so a SKU that belongs to
// another organization is reported as missing.
func (r *Repository) FindVariantBySKU(
	ctx context.Context,
	orgID uuid.UUID,
	sku string,
) (*MedicineVariant, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(sku) == "" {
		return nil, fmt.Errorf("%w: sku is required", ErrInvalidRequest)
	}

	const q = `SELECT ` + variantColumns + `
	FROM medicine_variants v
	JOIN medicines m ON m.id = v.medicine_id
	JOIN brands b ON b.id = m.brand_id
	WHERE v.sku = $1 AND v.deleted_at IS NULL AND b.organization_id = $2`

	found, err := scanVariant(r.db.ReadFromCtx(ctx).QueryRow(ctx, q, sku, orgID))
	if err != nil {
		return nil, fmt.Errorf("medicine variant find by sku: %w", err)
	}
	return found, nil
}

// ListVariants returns one keyset page of the package variants of a medicine.
//
// The variants are ordered by name with the id tie-breaker, and the cursor is
// bound to the medicine and to the tenant, so a cursor issued for one
// product cannot be replayed against another.
func (r *Repository) ListVariants(
	ctx context.Context,
	orgID, medicineID uuid.UUID,
	limit int,
	cursor string,
) (VariantPage, error) {
	if err := r.ready(); err != nil {
		return VariantPage{}, err
	}
	if orgID == uuid.Nil {
		return VariantPage{}, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if medicineID == uuid.Nil {
		return VariantPage{}, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if limit <= 0 {
		return VariantPage{}, fmt.Errorf("%w: limit is required", ErrInvalidRequest)
	}
	if r.cursorCodec == nil {
		return VariantPage{}, fmt.Errorf("%w: cursor signing is not configured", ErrInvalidRequest)
	}

	binding := variantCursorBinding(orgID, medicineID)

	var payload *cursorPayload
	if cursor != "" {
		decoded, decodeErr := r.cursorCodec.Decode(cursor)
		if decodeErr != nil {
			return VariantPage{}, decodeErr
		}
		if decoded.Binding != binding {
			return VariantPage{}, fmt.Errorf("%w: the cursor was issued for a different query", ErrInvalidCursor)
		}
		payload = &decoded
	}

	args := []any{medicineID, orgID}
	where := ` WHERE v.medicine_id = $1
	   AND v.deleted_at IS NULL
	   AND m.deleted_at IS NULL
	   AND b.organization_id = $2`

	if payload != nil {
		id, idErr := uuid.Parse(payload.ID)
		if idErr != nil {
			return VariantPage{}, fmt.Errorf("%w: the cursor id is malformed", ErrInvalidCursor)
		}
		args = append(args, payload.Key, id)
		where += fmt.Sprintf(" AND (v.name, v.id) > ($%d, $%d)", len(args)-1, len(args))
	}

	args = append(args, limit+1)
	listSQL := `SELECT ` + variantColumns + `
	FROM medicine_variants v
	JOIN medicines m ON m.id = v.medicine_id
	JOIN brands b ON b.id = m.brand_id` + where + `
	ORDER BY v.name ASC, v.id ASC
	LIMIT $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, args...)
	if err != nil {
		return VariantPage{}, fmt.Errorf("medicine variant list query: %w", err)
	}
	defer rows.Close()

	items := make([]MedicineVariant, 0, limit)
	hasMore := false
	var lastName string
	var lastID uuid.UUID
	for rows.Next() {
		item, err := scanVariant(rows)
		if err != nil {
			return VariantPage{}, fmt.Errorf("scan medicine variant: %w", err)
		}
		if len(items) == limit {
			hasMore = true
			break
		}
		items = append(items, *item)
		lastName = item.Name
		lastID = item.ID
	}
	if err := rows.Err(); err != nil {
		return VariantPage{}, fmt.Errorf("iterate medicine variant list: %w", err)
	}

	page := VariantPage{Items: items, HasMore: hasMore}
	if !hasMore || len(items) == 0 {
		return page, nil
	}
	next, err := r.cursorCodec.Encode(cursorPayload{
		Version: cursorVersion,
		Binding: binding,
		Field:   "name",
		Key:     lastName,
		ID:      lastID.String(),
	})
	if err != nil {
		return VariantPage{}, err
	}
	page.NextCursor = next
	return page, nil
}

// UpdateVariant writes the mutable columns of a variant and returns the
// refreshed row.
//
// sku and barcode are not written: they are the variant's identity and its
// exact-lookup keys, and moving a barcode between variants is exactly what
// the contract forbids (contract §5.3). mrp belongs to pricing.
func (r *Repository) UpdateVariant(
	ctx context.Context,
	orgID, medicineID uuid.UUID,
	v *MedicineVariant,
) (*MedicineVariant, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("%w: variant is required", ErrInvalidRequest)
	}
	if v.ID == uuid.Nil || medicineID == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id and variant id are required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	setClause, setValues := buildSetClause(4, []sqlAssignment{
		{"name", v.Name},
		{"strength", v.Strength},
		{"pack_size", v.PackSize},
		{"units_per_pack", v.UnitsPerPack},
		{"is_active", v.IsActive},
	})

	q := `UPDATE medicine_variants AS v SET ` + setClause + `
	WHERE v.id = $1
	  AND v.medicine_id = $2
	  AND v.deleted_at IS NULL
	  AND EXISTS (
		SELECT 1 FROM medicines m
		JOIN brands b ON b.id = m.brand_id
		WHERE m.id = v.medicine_id AND m.deleted_at IS NULL AND b.organization_id = $3
	  )
	RETURNING ` + variantColumns

	updated, err := scanVariant(r.db.FromCtx(ctx).QueryRow(ctx, q,
		append([]any{v.ID, medicineID, orgID}, setValues...)...,
	))
	if err != nil {
		return nil, fmt.Errorf("medicine variant update: %w", err)
	}
	return updated, nil
}

// SoftDeleteVariant marks a variant deleted and reports whether a row was
// affected.
//
// The row is only marked: purchase items, inventory batches and sale items
// reference it. The service decides whether a referenced variant may be
// removed at all — see VariantIsReferenced.
func (r *Repository) SoftDeleteVariant(
	ctx context.Context,
	orgID, medicineID, variantID uuid.UUID,
) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if medicineID == uuid.Nil || variantID == uuid.Nil {
		return false, fmt.Errorf("%w: medicine id and variant id are required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return false, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `UPDATE medicine_variants AS v SET deleted_at = now()
	WHERE v.id = $1
	  AND v.medicine_id = $2
	  AND v.deleted_at IS NULL
	  AND EXISTS (
		SELECT 1 FROM medicines m
		JOIN brands b ON b.id = m.brand_id
		WHERE m.id = v.medicine_id AND m.deleted_at IS NULL AND b.organization_id = $3
	  )`

	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, variantID, medicineID, orgID)
	if err != nil {
		return false, fmt.Errorf("medicine variant soft delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// VariantIsReferenced reports whether any operational record still points at
// the variant.
//
// A referenced variant keeps its pack size and units per pack: changing the
// conversion of a package that was already bought, stocked or sold would
// silently rewrite what those records mean. The service turns this into a
// conflict and asks for a new variant instead (contract §5.2, §10).
func (r *Repository) VariantIsReferenced(ctx context.Context, variantID uuid.UUID) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if variantID == uuid.Nil {
		return false, fmt.Errorf("%w: variant id is required", ErrInvalidRequest)
	}

	const q = `SELECT
		EXISTS (SELECT 1 FROM inventory_batches WHERE variant_id = $1)
		OR EXISTS (SELECT 1 FROM purchase_items   WHERE variant_id = $1)
		OR EXISTS (SELECT 1 FROM sale_items      WHERE variant_id = $1)`

	var referenced bool
	if err := r.db.ReadFromCtx(ctx).QueryRow(ctx, q, variantID).Scan(&referenced); err != nil {
		return false, fmt.Errorf("medicine variant reference check: %w", err)
	}
	return referenced, nil
}

// variantCursorBinding hashes the tenant and the medicine a variant cursor
// belongs to.
func variantCursorBinding(orgID, medicineID uuid.UUID) string {
	sum := sha256.Sum256([]byte("medicine:variant:v1:" + orgID.String() + ":" + medicineID.String()))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// medicine_barcodes
// ---------------------------------------------------------------------------

// BarcodePage is one page of barcode assignments.
type BarcodePage struct {
	Items      []MedicineBarcode
	NextCursor string
	HasMore    bool
}

// CreateBarcode binds a barcode to a medicine and, optionally, to one of its
// variants.
//
// A barcode that names a variant must name a variant of the same medicine:
// the statement checks it, so the table can never hold a barcode that points
// a variant of one product at another (contract §5.3).
func (r *Repository) CreateBarcode(
	ctx context.Context,
	orgID, medicineID uuid.UUID,
	b *MedicineBarcode,
) (*MedicineBarcode, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("%w: barcode is required", ErrInvalidRequest)
	}
	if medicineID == uuid.Nil {
		return nil, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	columns := []string{"medicine_id", "variant_id", "barcode", "barcode_type", "is_primary"}
	values := []any{medicineID, b.VariantID, b.Barcode, b.BarcodeType, b.IsPrimary}

	q := `INSERT INTO medicine_barcodes AS bc (` + strings.Join(columns, ", ") + `)
	SELECT ` + numberedPlaceholders(1, len(values)) + `
	FROM medicines m
	JOIN brands b ON b.id = m.brand_id
	WHERE m.id = $1
	  AND m.deleted_at IS NULL
	  AND b.organization_id = $` + strconv.Itoa(len(values)+1) + `
	  AND (
		$2::uuid IS NULL
		OR EXISTS (
			SELECT 1 FROM medicine_variants v
			WHERE v.id = $2 AND v.medicine_id = m.id AND v.deleted_at IS NULL
		)
	  )
	RETURNING ` + barcodeColumns

	created, err := scanBarcode(r.db.FromCtx(ctx).QueryRow(ctx, q, append(values, orgID)...))
	if err != nil {
		return nil, fmt.Errorf("medicine barcode create: %w", err)
	}
	return created, nil
}

// ListBarcodes returns one keyset page of the barcode assignments of a
// medicine.
//
// The assignments are ordered by barcode with the id tie-breaker, and the
// cursor is bound to the medicine and to the tenant.
func (r *Repository) ListBarcodes(
	ctx context.Context,
	orgID, medicineID uuid.UUID,
	limit int,
	cursor string,
) (BarcodePage, error) {
	if err := r.ready(); err != nil {
		return BarcodePage{}, err
	}
	if orgID == uuid.Nil {
		return BarcodePage{}, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if medicineID == uuid.Nil {
		return BarcodePage{}, fmt.Errorf("%w: medicine id is required", ErrInvalidRequest)
	}
	if limit <= 0 {
		return BarcodePage{}, fmt.Errorf("%w: limit is required", ErrInvalidRequest)
	}
	if r.cursorCodec == nil {
		return BarcodePage{}, fmt.Errorf("%w: cursor signing is not configured", ErrInvalidRequest)
	}

	binding := barcodeCursorBinding(orgID, medicineID)

	var payload *cursorPayload
	if cursor != "" {
		decoded, decodeErr := r.cursorCodec.Decode(cursor)
		if decodeErr != nil {
			return BarcodePage{}, decodeErr
		}
		if decoded.Binding != binding {
			return BarcodePage{}, fmt.Errorf("%w: the cursor was issued for a different query", ErrInvalidCursor)
		}
		payload = &decoded
	}

	args := []any{medicineID, orgID}
	where := ` WHERE bc.medicine_id = $1
	   AND bc.deleted_at IS NULL
	   AND m.deleted_at IS NULL
	   AND b.organization_id = $2`

	if payload != nil {
		id, idErr := uuid.Parse(payload.ID)
		if idErr != nil {
			return BarcodePage{}, fmt.Errorf("%w: the cursor id is malformed", ErrInvalidCursor)
		}
		args = append(args, payload.Key, id)
		where += fmt.Sprintf(" AND (bc.barcode, bc.id) > ($%d, $%d)", len(args)-1, len(args))
	}

	args = append(args, limit+1)
	listSQL := `SELECT ` + barcodeColumns + `
	FROM medicine_barcodes bc
	JOIN medicines m ON m.id = bc.medicine_id
	JOIN brands b ON b.id = m.brand_id` + where + `
	ORDER BY bc.barcode ASC, bc.id ASC
	LIMIT $` + strconv.Itoa(len(args))

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, listSQL, args...)
	if err != nil {
		return BarcodePage{}, fmt.Errorf("medicine barcode list query: %w", err)
	}
	defer rows.Close()

	items := make([]MedicineBarcode, 0, limit)
	hasMore := false
	var lastBarcode string
	var lastID uuid.UUID
	for rows.Next() {
		item, err := scanBarcode(rows)
		if err != nil {
			return BarcodePage{}, fmt.Errorf("scan medicine barcode: %w", err)
		}
		if len(items) == limit {
			hasMore = true
			break
		}
		items = append(items, *item)
		lastBarcode = item.Barcode
		lastID = item.ID
	}
	if err := rows.Err(); err != nil {
		return BarcodePage{}, fmt.Errorf("iterate medicine barcode list: %w", err)
	}

	page := BarcodePage{Items: items, HasMore: hasMore}
	if !hasMore || len(items) == 0 {
		return page, nil
	}
	next, err := r.cursorCodec.Encode(cursorPayload{
		Version: cursorVersion,
		Binding: binding,
		Field:   "barcode",
		Key:     lastBarcode,
		ID:      lastID.String(),
	})
	if err != nil {
		return BarcodePage{}, err
	}
	page.NextCursor = next
	return page, nil
}

// DeactivateBarcode marks a barcode assignment deleted and reports whether a
// row was affected.
//
// The assignment is deactivated, not erased: the history of what was scanned
// has to stay readable (contract §8.2, §10).
func (r *Repository) DeactivateBarcode(
	ctx context.Context,
	orgID, medicineID, barcodeID uuid.UUID,
) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if medicineID == uuid.Nil || barcodeID == uuid.Nil {
		return false, fmt.Errorf("%w: medicine id and barcode id are required", ErrInvalidRequest)
	}
	if orgID == uuid.Nil {
		return false, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}

	const q = `UPDATE medicine_barcodes AS bc SET deleted_at = now()
	WHERE bc.id = $1
	  AND bc.medicine_id = $2
	  AND bc.deleted_at IS NULL
	  AND EXISTS (
		SELECT 1 FROM medicines m
		JOIN brands b ON b.id = m.brand_id
		WHERE m.id = bc.medicine_id AND m.deleted_at IS NULL AND b.organization_id = $3
	  )`

	tag, err := r.db.FromCtx(ctx).Exec(ctx, q, barcodeID, medicineID, orgID)
	if err != nil {
		return false, fmt.Errorf("medicine barcode deactivate: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// barcodeCursorBinding hashes the tenant and the medicine a barcode cursor
// belongs to.
func barcodeCursorBinding(orgID, medicineID uuid.UUID) string {
	sum := sha256.Sum256([]byte("medicine:barcode:v1:" + orgID.String() + ":" + medicineID.String()))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Exact lookup
// ---------------------------------------------------------------------------

// LookupCandidate is one exact-lookup match.
//
// It carries catalogue and variant metadata only. It does not carry stock
// availability and it does not carry a price: a catalogue lookup does not
// prove stock, and the sale workflow revalidates branch stock, price and
// safety state itself (contract §7, §8.2).
type LookupCandidate struct {
	MedicineID uuid.UUID
	VariantID  *uuid.UUID

	MedicineName string
	VariantName  *string
	BrandName    *string

	SKU          *string
	Barcode      *string
	Strength     *string
	PackSize     *string
	UnitsPerPack *int

	RequiresPrescription bool
	IsControlled         bool
	IsActive             bool
}

// lookupProjection is the projection shared by the two exact lookups: the
// medicine, its optional variant and the labels a POS screen needs.
//
// The variant columns are nullable because a lookup may resolve to the
// medicine itself, when the identifier is the medicine-level SKU or barcode.
const lookupProjection = `SELECT DISTINCT
		m.id, m.name, m.requires_prescription, m.is_controlled, m.is_active,
		v.id, v.name, v.sku, v.strength, v.pack_size, v.units_per_pack, v.is_active,
		b.name, m.barcode, v.barcode
	FROM hits h
	JOIN medicines m ON m.id = h.medicine_id AND m.deleted_at IS NULL
	JOIN brands b ON b.id = m.brand_id AND b.organization_id = $2
	LEFT JOIN medicine_variants v ON v.id = h.variant_id AND v.deleted_at IS NULL`

// scanLookupCandidate reads one lookup row.
func scanLookupCandidate(row rowScanner) (LookupCandidate, error) {
	var (
		c            LookupCandidate
		variantID    *uuid.UUID
		variantName  *string
		variantSKU   *string
		strength     *string
		packSize     *string
		unitsPerPack *int
		brandName    string
		medicineCode *string
		variantCode  *string
		variantLive  *bool
	)
	if err := row.Scan(
		&c.MedicineID, &c.MedicineName, &c.RequiresPrescription, &c.IsControlled, &c.IsActive,
		&variantID, &variantName, &variantSKU, &strength, &packSize, &unitsPerPack, &variantLive,
		&brandName, &medicineCode, &variantCode,
	); err != nil {
		return LookupCandidate{}, err
	}
	c.VariantID = variantID
	c.VariantName = variantName
	c.BrandName = &brandName
	c.Strength = strength
	c.PackSize = packSize
	c.UnitsPerPack = unitsPerPack
	c.Barcode = variantCode
	if c.Barcode == nil {
		c.Barcode = medicineCode
	}
	if variantSKU != nil {
		c.SKU = variantSKU
	}
	return c, nil
}

// LookupByBarcode resolves a barcode to exactly one active variant.
//
// The barcode may live in three places: the medicine_barcodes table (with or
// without a variant), the medicine-level barcode column and the variant-level
// barcode column. All three are resolved to (medicine, variant) pairs and
// de-duplicated, so a barcode that is stored twice for the same pair is not
// an ambiguity.
//
// A barcode that still matches more than one pair is an integrity error and
// is reported as ErrAmbiguousIdentifier: POS has to fail closed rather than
// sell the wrong product (contract §5.3).
func (r *Repository) LookupByBarcode(
	ctx context.Context,
	orgID uuid.UUID,
	barcode string,
) (LookupCandidate, error) {
	if err := r.ready(); err != nil {
		return LookupCandidate{}, err
	}
	if orgID == uuid.Nil {
		return LookupCandidate{}, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(barcode) == "" {
		return LookupCandidate{}, fmt.Errorf("%w: barcode is required", ErrInvalidRequest)
	}

	// Only an active variant is eligible for a POS lookup (contract §8.2), so
	// the hits are filtered on the variant state rather than on the join
	// afterwards: a code that belongs to an inactive variant must not fall
	// back to the medicine-level candidate.
	const q = `WITH hits AS (
		SELECT mb.medicine_id, mb.variant_id
		  FROM medicine_barcodes mb
		  JOIN medicine_variants mv
		    ON mv.id = mb.variant_id AND mv.deleted_at IS NULL AND mv.is_active = TRUE
		 WHERE mb.barcode = $1 AND mb.deleted_at IS NULL
		UNION
		SELECT mb.medicine_id, NULL::uuid
		  FROM medicine_barcodes mb
		 WHERE mb.barcode = $1 AND mb.deleted_at IS NULL AND mb.variant_id IS NULL
		UNION
		SELECT m.id, NULL::uuid
		  FROM medicines m
		 WHERE m.barcode = $1 AND m.deleted_at IS NULL
		UNION
		SELECT v.medicine_id, v.id
		  FROM medicine_variants v
		 WHERE v.barcode = $1 AND v.deleted_at IS NULL AND v.is_active = TRUE
	)` + lookupProjection

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, barcode, orgID)
	if err != nil {
		return LookupCandidate{}, fmt.Errorf("medicine barcode lookup: %w", err)
	}
	defer rows.Close()

	var candidates []LookupCandidate
	for rows.Next() {
		candidate, err := scanLookupCandidate(rows)
		if err != nil {
			return LookupCandidate{}, fmt.Errorf("scan lookup candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return LookupCandidate{}, fmt.Errorf("iterate lookup candidates: %w", err)
	}

	return resolveLookupCandidates(candidates, "barcode", barcode)
}

// LookupBySKU resolves a SKU to exactly one active variant or medicine.
//
// A SKU may belong to a variant or to a medicine; a SKU that matches both is
// ambiguous and is reported as ErrAmbiguousIdentifier.
func (r *Repository) LookupBySKU(
	ctx context.Context,
	orgID uuid.UUID,
	sku string,
) (LookupCandidate, error) {
	if err := r.ready(); err != nil {
		return LookupCandidate{}, err
	}
	if orgID == uuid.Nil {
		return LookupCandidate{}, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(sku) == "" {
		return LookupCandidate{}, fmt.Errorf("%w: sku is required", ErrInvalidRequest)
	}

	// Only an active variant is eligible for a POS lookup (contract §8.2).
	const q = `WITH hits AS (
		SELECT v.medicine_id, v.id
		  FROM medicine_variants v
		 WHERE v.sku = $1 AND v.deleted_at IS NULL AND v.is_active = TRUE
		UNION
		SELECT m.id, NULL::uuid
		  FROM medicines m
		 WHERE m.sku = $1 AND m.deleted_at IS NULL
	)` + lookupProjection

	rows, err := r.db.ReadFromCtx(ctx).Query(ctx, q, sku, orgID)
	if err != nil {
		return LookupCandidate{}, fmt.Errorf("medicine sku lookup: %w", err)
	}
	defer rows.Close()

	var candidates []LookupCandidate
	for rows.Next() {
		candidate, err := scanLookupCandidate(rows)
		if err != nil {
			return LookupCandidate{}, fmt.Errorf("scan lookup candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return LookupCandidate{}, fmt.Errorf("iterate lookup candidates: %w", err)
	}

	return resolveLookupCandidates(candidates, "sku", sku)
}

// resolveLookupCandidates turns the matched rows into exactly one candidate.
//
// No match is a not-found (db.ErrNoRows) and more than one match is an
// integrity error: the sale workflow must never have to guess which product
// an identifier means.
func resolveLookupCandidates(candidates []LookupCandidate, kind, value string) (LookupCandidate, error) {
	switch len(candidates) {
	case 0:
		return LookupCandidate{}, db.ErrNoRows
	case 1:
		return candidates[0], nil
	default:
		return LookupCandidate{}, fmt.Errorf("%w: %s %q", ErrAmbiguousIdentifier, kind, value)
	}
}

// ---------------------------------------------------------------------------
// Statement builders
//
// The placeholders of the write statements are derived from the column list
// instead of being written by hand. A hand-written placeholder can drift
// away from the value it is supposed to carry, and the statement still
// compiles, still runs and still writes the wrong value into the wrong
// column. Deriving them makes that impossible.
// ---------------------------------------------------------------------------

// sqlAssignment is one column assignment of an UPDATE statement.
type sqlAssignment struct {
	column string
	value  any
}

// buildSetClause renders the SET clause of an UPDATE and returns the values
// in the order the placeholders use.
//
// The first placeholder is start, so the caller can keep the row identity
// and the tenant scope in front of the assigned values.
func buildSetClause(start int, assignments []sqlAssignment) (string, []any) {
	sets := make([]string, 0, len(assignments))
	values := make([]any, 0, len(assignments))
	for _, assignment := range assignments {
		values = append(values, assignment.value)
		sets = append(sets, assignment.column+" = $"+strconv.Itoa(start+len(values)-1))
	}
	return strings.Join(sets, ", "), values
}

// numberedPlaceholders renders $start..$start+count-1.
func numberedPlaceholders(start, count int) string {
	parts := make([]string, 0, count)
	for i := 0; i < count; i++ {
		parts = append(parts, "$"+strconv.Itoa(start+i))
	}
	return strings.Join(parts, ", ")
}

// placeholderOf returns the placeholder of a named column in a value list.
func placeholderOf(columns []string, name string, start int) (string, error) {
	for i, column := range columns {
		if column == name {
			return "$" + strconv.Itoa(start+i), nil
		}
	}
	return "", fmt.Errorf("%w: the column %q is not part of the statement", ErrInvalidRequest, name)
}

// ---------------------------------------------------------------------------
// Query builders and helpers
// ---------------------------------------------------------------------------

// buildMedicineFilters builds the tenant-scoped WHERE clause and its
// arguments.
//
// The tenant scope is the first condition and the first argument, so every
// statement that uses the builder is scoped by construction. The free-text
// term is matched with ILIKE and the '!' escape character, so a term that
// contains a wildcard is matched literally instead of turning the query into
// an unbounded scan.
func buildMedicineFilters(q *ListMedicinesQuery, orgID uuid.UUID) (string, []any) {
	conditions := []string{"b.organization_id = $1", "m.deleted_at IS NULL"}
	args := []any{orgID}

	add := func(clause string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(clause, len(args)))
	}

	if id := q.CategoryUUID(); id != nil {
		add("m.category_id = $%d", *id)
	}
	if id := q.BrandUUID(); id != nil {
		add("m.brand_id = $%d", *id)
	}
	if id := q.GenericUUID(); id != nil {
		add("m.generic_medicine_id = $%d", *id)
	}
	if q.IsActive != nil {
		add("m.is_active = $%d", *q.IsActive)
	}
	if q.RequiresPrescription != nil {
		add("m.requires_prescription = $%d", *q.RequiresPrescription)
	}
	if q.IsControlled != nil {
		add("m.is_controlled = $%d", *q.IsControlled)
	}
	if term := q.NormalizedQ(); term != "" {
		add(
			"(m.name ILIKE $%[1]d ESCAPE '!' OR m.display_name ILIKE $%[1]d ESCAPE '!' "+
				"OR m.sku ILIKE $%[1]d ESCAPE '!' OR m.barcode ILIKE $%[1]d ESCAPE '!')",
			likePattern(term),
		)
	}

	return " WHERE " + strings.Join(conditions, " AND "), args
}

// buildMedicineOrderBy renders the ORDER BY clause of the offset list.
//
// The sort fields come from the parsed query string and are checked against
// the allow-list, so a client can never inject an expression. The id
// tie-breaker is always appended, so the order is total and a page boundary
// is unambiguous.
func buildMedicineOrderBy(sort []pagination.SortField) (string, error) {
	if len(sort) == 0 {
		return "m.name ASC, m.id ASC", nil
	}
	parts := make([]string, 0, len(sort)+1)
	hasID := false
	for _, field := range sort {
		if _, allowed := allowedMedicineSortFields[field.Field]; !allowed {
			return "", fmt.Errorf("%w: unsupported sort field %q", ErrInvalidRequest, field.Field)
		}
		direction := "ASC"
		if field.Desc {
			direction = "DESC"
		}
		parts = append(parts, "m."+field.Field+" "+direction)
		if field.Field == "id" {
			hasID = true
		}
	}
	if !hasID {
		parts = append(parts, "m.id ASC")
	}
	return strings.Join(parts, ", "), nil
}

// likePattern turns a search term into an ILIKE pattern.
//
// The wildcards of the pattern language are escaped, so a term that contains
// '%' or '_' is matched literally and cannot widen the search.
func likePattern(term string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return "%" + replacer.Replace(term) + "%"
}

// ready reports whether the repository can run a statement.
func (r *Repository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("%w: repository database is not configured", ErrInvalidRequest)
	}
	return nil
}
