package medicine

// Service-level tests.
//
// The service talks to a concrete repository, so these tests cover the parts
// that are pure business rules: the patch semantics, the reason requirement
// for regulatory changes, the detection of a semantic change, the validation
// of the written values and the translation of repository errors into the
// shared error envelope (contract §14, "Unit").

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	appctx "backend/internal/common/context"
	errs "backend/internal/errors"
	"backend/internal/platform/db"
)

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func intPtr(i int) *int { return &i }

// ---------------------------------------------------------------------------
// Patch semantics
// ---------------------------------------------------------------------------

// TestApplyMedicinePatchTreatsOmittedFieldsAsUnchanged is the PATCH contract:
// a nil pointer means "leave unchanged", so an omitted field is never
// confused with an explicit zero, false or empty value.
func TestApplyMedicinePatchTreatsOmittedFieldsAsUnchanged(t *testing.T) {
	m := &Medicine{
		SKU:        "NAP-500-10",
		Name:       "Napa 500 mg",
		BrandID:    uuid.New(),
		CategoryID: uuid.New(),
		IsActive:   true,
		Strength:   strPtr("500 mg"),
	}

	applyMedicinePatch(m, &UpdateMedicineRequest{})

	if m.Name != "Napa 500 mg" || m.Strength == nil || *m.Strength != "500 mg" || !m.IsActive {
		t.Fatalf("an empty patch must not change the model: %+v", m)
	}
}

// TestApplyMedicinePatchAppliesExplicitValues checks that an explicit value —
// including false — is a real change.
func TestApplyMedicinePatchAppliesExplicitValues(t *testing.T) {
	m := &Medicine{Name: "Napa 500 mg", IsActive: true, RequiresPrescription: true}

	applyMedicinePatch(m, &UpdateMedicineRequest{
		Name:                 strPtr("  Napa Extra  "),
		IsActive:             boolPtr(false),
		RequiresPrescription: boolPtr(false),
	})

	if m.Name != "Napa Extra" {
		t.Fatalf("expected the trimmed name, got %q", m.Name)
	}
	if m.IsActive {
		t.Fatal("an explicit is_active=false must be applied")
	}
	if m.RequiresPrescription {
		t.Fatal("an explicit requires_prescription=false must be applied")
	}
}

// TestApplyMedicinePatchNeverRewritesTheIdentityKeys is the write-once rule:
// the SKU, the barcode and the brand are not part of the update DTO, so a
// patch can never move a product in the catalogue (contract §5.3, §10).
func TestApplyMedicinePatchNeverRewritesTheIdentityKeys(t *testing.T) {
	brandID := uuid.New()
	m := &Medicine{
		SKU:     "NAP-500-10",
		Barcode: strPtr("8901234567890"),
		BrandID: brandID,
		Name:    "Napa 500 mg",
	}

	applyMedicinePatch(m, &UpdateMedicineRequest{
		Name: strPtr("Napa 500 mg"),
	})

	if m.SKU != "NAP-500-10" {
		t.Fatalf("the sku must not change, got %q", m.SKU)
	}
	if m.Barcode == nil || *m.Barcode != "8901234567890" {
		t.Fatalf("the barcode must not change, got %v", m.Barcode)
	}
	if m.BrandID != brandID {
		t.Fatalf("the brand must not change, got %s", m.BrandID)
	}
}

// ---------------------------------------------------------------------------
// Regulatory reason
// ---------------------------------------------------------------------------

// TestRegulatoryChangeRequiresAReason locks the rule that a regulatory or
// import-provenance change has to be justified (contract §5.4, §11).
func TestRegulatoryChangeRequiresAReason(t *testing.T) {
	svc := &Service{}

	cases := []struct {
		name    string
		mutate  func(*UpdateMedicineRequest)
		current Medicine
	}{
		{
			name:    "prescription flag",
			mutate:  func(r *UpdateMedicineRequest) { r.RequiresPrescription = boolPtr(true) },
			current: Medicine{},
		},
		{
			name:    "controlled flag",
			mutate:  func(r *UpdateMedicineRequest) { r.IsControlled = boolPtr(true) },
			current: Medicine{},
		},
		{
			name:    "refrigerated flag",
			mutate:  func(r *UpdateMedicineRequest) { r.IsRefrigerated = boolPtr(true) },
			current: Medicine{},
		},
		{
			name:    "imported flag",
			mutate:  func(r *UpdateMedicineRequest) { r.IsImported = boolPtr(true) },
			current: Medicine{},
		},
		{
			name:    "country of origin",
			mutate:  func(r *UpdateMedicineRequest) { r.CountryOfOrigin = strPtr("Bangladesh") },
			current: Medicine{},
		},
		{
			name:    "importer name",
			mutate:  func(r *UpdateMedicineRequest) { r.ImporterName = strPtr("Pharma Ltd") },
			current: Medicine{},
		},
		{
			name:    "import licence",
			mutate:  func(r *UpdateMedicineRequest) { r.ImportLicenseNo = strPtr("LIC-1") },
			current: Medicine{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &UpdateMedicineRequest{}
			tc.mutate(req)

			err := svc.requireReasonForRegulatoryChange(&tc.current, req)
			if err == nil {
				t.Fatal("expected the change to be refused without a reason")
			}
			if !errs.Has(err, errs.CodeValidationError) {
				t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeValidationError)
			}

			req.Reason = strPtr("regulatory review 2026-10")
			if err := svc.requireReasonForRegulatoryChange(&tc.current, req); err != nil {
				t.Fatalf("a change with a reason must be accepted: %v", err)
			}
		})
	}
}

// TestUnchangedRegulatoryValueNeedsNoReason checks that a patch that repeats
// the current value is not a change: only an actual modification needs a
// reason.
func TestUnchangedRegulatoryValueNeedsNoReason(t *testing.T) {
	svc := &Service{}
	m := &Medicine{IsControlled: true, CountryOfOrigin: strPtr("Bangladesh")}

	req := &UpdateMedicineRequest{
		IsControlled:    boolPtr(true),
		CountryOfOrigin: strPtr("Bangladesh"),
	}
	if err := svc.requireReasonForRegulatoryChange(m, req); err != nil {
		t.Fatalf("an unchanged value must not require a reason: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Semantic protection
// ---------------------------------------------------------------------------

// TestSemanticChangeRequestedDetectsMeaningChanges covers the fields whose
// change would rewrite what historical stock, purchases and sales referred to.
func TestSemanticChangeRequestedDetectsMeaningChanges(t *testing.T) {
	m := &Medicine{
		Strength:          strPtr("500 mg"),
		PackSize:          strPtr("10x10"),
		UnitsPerPack:      intPtr(100),
		UnitsPerStrip:     intPtr(10),
		GenericMedicineID: uuidPtr(),
	}

	cases := []struct {
		name string
		req  *UpdateMedicineRequest
		want bool
	}{
		{name: "no change", req: &UpdateMedicineRequest{}, want: false},
		{name: "name", req: &UpdateMedicineRequest{Name: strPtr("Napa Extra")}, want: false},
		{name: "description", req: &UpdateMedicineRequest{Description: strPtr("Pain relief")}, want: false},
		{name: "strength", req: &UpdateMedicineRequest{Strength: strPtr("250 mg")}, want: true},
		{name: "pack size", req: &UpdateMedicineRequest{PackSize: strPtr("20x10")}, want: true},
		{name: "units per pack", req: &UpdateMedicineRequest{UnitsPerPack: intPtr(50)}, want: true},
		{name: "units per strip", req: &UpdateMedicineRequest{UnitsPerStrip: intPtr(5)}, want: true},
		{name: "generic", req: &UpdateMedicineRequest{GenericMedicineID: uuidPtr()}, want: true},
		{name: "same strength", req: &UpdateMedicineRequest{Strength: strPtr("500 mg")}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := semanticChangeRequested(m, tc.req); got != tc.want {
				t.Fatalf("semanticChangeRequested = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Written values
// ---------------------------------------------------------------------------

// TestValidateMedicineTextRejectsOversizedValues checks the schema widths on
// the values the service writes.
func TestValidateMedicineTextRejectsOversizedValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Medicine)
	}{
		{name: "sku", mutate: func(m *Medicine) { m.SKU = strings.Repeat("a", maxSKULen+1) }},
		{name: "barcode", mutate: func(m *Medicine) { m.Barcode = strPtr(strings.Repeat("b", maxBarcodeLen+1)) }},
		{name: "name", mutate: func(m *Medicine) { m.Name = strings.Repeat("n", maxNameLen+1) }},
		{name: "display name", mutate: func(m *Medicine) { m.DisplayName = strPtr(strings.Repeat("d", maxDisplayNameLen+1)) }},
		{name: "description", mutate: func(m *Medicine) { m.Description = strPtr(strings.Repeat("x", maxDescriptionLen+1)) }},
		{name: "strength", mutate: func(m *Medicine) { m.Strength = strPtr(strings.Repeat("s", maxStrengthLen+1)) }},
		{name: "pack size", mutate: func(m *Medicine) { m.PackSize = strPtr(strings.Repeat("p", maxPackSizeLen+1)) }},
		{name: "country", mutate: func(m *Medicine) { m.CountryOfOrigin = strPtr(strings.Repeat("c", maxCountryOfOriginLen+1)) }},
		{name: "importer", mutate: func(m *Medicine) { m.ImporterName = strPtr(strings.Repeat("i", maxImporterNameLen+1)) }},
		{name: "licence", mutate: func(m *Medicine) { m.ImportLicenseNo = strPtr(strings.Repeat("l", maxImportLicenseNoLen+1)) }},
		{name: "hsn", mutate: func(m *Medicine) { m.HSNCode = strPtr(strings.Repeat("h", maxHSNCodeLen+1)) }},
		{name: "image url", mutate: func(m *Medicine) { m.ImageURL = strPtr(strings.Repeat("u", maxImageURLLen+1)) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Medicine{SKU: "NAP-500-10", Name: "Napa 500 mg"}
			tc.mutate(m)
			err := validateMedicineText(m)
			if err == nil {
				t.Fatal("expected an oversized value to be rejected")
			}
			if !errs.Has(err, errs.CodeValidationError) {
				t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeValidationError)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// TestMapMedicineReadErrorMapsMissingRowsToNotFound.
func TestMapMedicineReadErrorMapsMissingRowsToNotFound(t *testing.T) {
	err := mapMedicineReadError(fmt.Errorf("medicine find by id: %w", db.ErrNoRows))
	if !errs.Has(err, errs.CodeNotFound) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeNotFound)
	}
}

// TestMapMedicineReadErrorMapsACursorToInvalidCursor.
func TestMapMedicineReadErrorMapsACursorToInvalidCursor(t *testing.T) {
	err := mapMedicineReadError(fmt.Errorf("%w: the cursor was tampered with", ErrInvalidCursor))
	if !errs.Has(err, errs.CodeInvalidCursor) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeInvalidCursor)
	}
}

// TestMapMedicineWriteErrorMapsUniqueViolationsToConflict is the duplicate
// SKU/barcode case (contract §10, "map unique violations to stable conflict
// errors").
func TestMapMedicineWriteErrorMapsUniqueViolationsToConflict(t *testing.T) {
	err := mapMedicineWriteError(&pgconn.PgError{Code: "23505", ConstraintName: "ux_medicines_sku"})
	if !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeConflict)
	}
}

// TestMapMedicineWriteErrorReportsAnInactiveBrand is the brand trigger's own
// constraint: the brand was deactivated between the check and the write.
func TestMapMedicineWriteErrorReportsAnInactiveBrand(t *testing.T) {
	err := mapMedicineWriteError(&pgconn.PgError{Code: "23503", ConstraintName: "medicines_brand_active_fkey"})
	if !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeConflict)
	}
	if !strings.Contains(err.Error(), "brand") {
		t.Fatalf("expected the message to mention the brand, got %q", err.Error())
	}
}

// TestMapLookupErrorMapsAmbiguityToConflict: a barcode that matches more
// than one active product has to fail closed (contract §5.3).
func TestMapLookupErrorMapsAmbiguityToConflict(t *testing.T) {
	err := mapLookupError(fmt.Errorf("%w: sku %q", ErrAmbiguousIdentifier, "NAP-500"))
	if !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeConflict)
	}
}

// TestMapLookupErrorMapsMissingRowsToNotFound.
func TestMapLookupErrorMapsMissingRowsToNotFound(t *testing.T) {
	err := mapLookupError(errNoRows())
	if !errs.Has(err, errs.CodeNotFound) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeNotFound)
	}
}

// ---------------------------------------------------------------------------
// Aggregate create (medicine + initial variants)
// ---------------------------------------------------------------------------

// TestBuildVariantNormalizesTheRequest: a variant is written with trimmed
// values, an active default and no medicine of its own — the medicine is
// bound by the repository statement.
func TestBuildVariantNormalizesTheRequest(t *testing.T) {
	v, err := buildVariant(&CreateMedicineVariantRequest{
		Name:         "  10 tablets x 10 strips  ",
		SKU:          "  NAP-500-10  ",
		Barcode:      strPtr("  8901234567890 "),
		Strength:     strPtr(" 500 mg "),
		PackSize:     strPtr(" 10x10 "),
		UnitsPerPack: intPtr(100),
	})
	if err != nil {
		t.Fatalf("buildVariant: %v", err)
	}
	if v.Name != "10 tablets x 10 strips" || v.SKU != "NAP-500-10" {
		t.Fatalf("expected the trimmed identity, got %+v", v)
	}
	if v.Barcode == nil || *v.Barcode != "8901234567890" {
		t.Fatalf("expected the trimmed barcode, got %v", v.Barcode)
	}
	if !v.IsActive {
		t.Fatal("a variant is active unless it says otherwise")
	}
	if v.UnitsPerPack == nil || *v.UnitsPerPack != 100 {
		t.Fatalf("expected 100 units per pack, got %v", v.UnitsPerPack)
	}
}

// TestBuildVariantRejectsAMissingIdentity: a variant without a name or a SKU
// has no package identity, so it is not a catalogue entry.
func TestBuildVariantRejectsAMissingIdentity(t *testing.T) {
	if _, err := buildVariant(&CreateMedicineVariantRequest{SKU: "NAP-500-10"}); !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeValidationError)
	}
	if _, err := buildVariant(&CreateMedicineVariantRequest{Name: "10 tablets"}); !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeValidationError)
	}
}

// TestBuildVariantRejectsOversizedValues: the API must not let a value
// through that the database would then truncate.
func TestBuildVariantRejectsOversizedValues(t *testing.T) {
	_, err := buildVariant(&CreateMedicineVariantRequest{
		Name: strings.Repeat("a", maxVariantNameLen+1),
		SKU:  "NAP-500-10",
	})
	if !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeValidationError)
	}
}

// TestEnsureVariantSKUFreeRejectsASKUTakenInTheSameRequest: two inline
// variants cannot claim one SKU, and the check happens before the database
// is touched.
func TestEnsureVariantSKUFreeRejectsASKUTakenInTheSameRequest(t *testing.T) {
	s := &Service{}
	taken := map[string]struct{}{"NAP-500-10": {}}

	err := s.ensureVariantSKUFree(context.Background(), uuid.New(), "NAP-500-10", taken)
	if !errs.Has(err, errs.CodeConflict) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeConflict)
	}
}

// ---------------------------------------------------------------------------
// Tenant scope
// ---------------------------------------------------------------------------

// TestOrganizationIDComesFromTheContextOnly: the tenant is never taken from
// the request (contract §3).
func TestOrganizationIDComesFromTheContextOnly(t *testing.T) {
	if _, err := organizationID(context.Background()); !errs.Has(err, errs.CodeUnauthenticated) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeUnauthenticated)
	}

	orgID := uuid.New()
	ctx := appctx.WithPrincipal(context.Background(), appctx.Principal{OrgID: orgID, UserID: uuid.New()})
	got, err := organizationID(ctx)
	if err != nil {
		t.Fatalf("organizationID: %v", err)
	}
	if got != orgID {
		t.Fatalf("organizationID = %s, want %s", got, orgID)
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// TestTrimOptionalDropsBlankValues: a blank string is no value, so it must
// not overwrite a stored value with an empty one.
func TestTrimOptionalDropsBlankValues(t *testing.T) {
	if trimOptional(nil) != nil {
		t.Fatal("a nil value stays nil")
	}
	if trimOptional(strPtr("   ")) != nil {
		t.Fatal("a blank value becomes nil")
	}
	trimmed := trimOptional(strPtr("  napa  "))
	if trimmed == nil || *trimmed != "napa" {
		t.Fatalf("expected the trimmed value, got %v", trimmed)
	}
}

// TestValidationErrorCarriesTheFieldDetails: the client has to learn which
// field failed, not just that something failed.
func TestValidationErrorCarriesTheFieldDetails(t *testing.T) {
	err := validationError("invalid query", []error{
		newFieldError("brand_id", "uuid", "must be a valid UUID"),
	})
	if !errs.Has(err, errs.CodeValidationError) {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeValidationError)
	}
	appErr := errs.As(err)
	if appErr == nil || len(appErr.Details) != 1 {
		t.Fatalf("expected one field detail, got %+v", appErr)
	}
	if appErr.Details[0].Field != "brand_id" || appErr.Details[0].Rule != "uuid" {
		t.Fatalf("unexpected field detail: %+v", appErr.Details[0])
	}
}

// TestReferenceResourceNamesAreHumanReadable: the not-found message names the
// resource, not the table.
func TestReferenceResourceNamesAreHumanReadable(t *testing.T) {
	if got := referenceResource("product_categories"); got != "product category" {
		t.Fatalf("got %q", got)
	}
	if got := referenceResource("unit_types"); got != "unit type" {
		t.Fatalf("got %q", got)
	}
	if got := referenceResource("tax_rates"); got != "tax rate" {
		t.Fatalf("got %q", got)
	}
}

// uuidPtr returns a fresh identifier.
func uuidPtr() *uuid.UUID {
	id := uuid.New()
	return &id
}

// errNoRows returns the repository's "no rows" sentinel.
func errNoRows() error {
	return db.ErrNoRows
}
