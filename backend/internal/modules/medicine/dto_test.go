package medicine

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"backend/internal/platform/validator"
)

func newMedicineTestValidator(t *testing.T) *validator.Validator {
	t.Helper()
	v, err := validator.New()
	if err != nil {
		t.Fatalf("validator.New: %v", err)
	}
	return v
}

// validCreate returns a minimal acceptable create request.
func validCreate() CreateMedicineRequest {
	return CreateMedicineRequest{
		SKU:        "NAP-500-10",
		Name:       "Napa 500 mg",
		BrandID:    uuid.New(),
		CategoryID: uuid.New(),
	}
}

// failingFields returns the set of field names that failed validation.
func failingFields(t *testing.T, err error) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if err == nil {
		return out
	}
	fe, ok := validator.AsFieldErrors(err)
	if !ok {
		t.Fatalf("expected field errors, got %v", err)
	}
	for _, f := range fe {
		out[f.Field] = true
	}
	return out
}

func TestCreateMedicineRequestValidation(t *testing.T) {
	v := newMedicineTestValidator(t)

	tests := []struct {
		name      string
		mutate    func(*CreateMedicineRequest)
		wantField string
	}{
		{name: "valid", mutate: func(*CreateMedicineRequest) {}},
		{name: "missing sku", mutate: func(r *CreateMedicineRequest) { r.SKU = "" }, wantField: "sku"},
		{name: "blank sku", mutate: func(r *CreateMedicineRequest) { r.SKU = "   " }, wantField: "sku"},
		{name: "sku too long", mutate: func(r *CreateMedicineRequest) { r.SKU = strings.Repeat("a", 61) }, wantField: "sku"},
		{name: "missing name", mutate: func(r *CreateMedicineRequest) { r.Name = "" }, wantField: "name"},
		{name: "missing brand", mutate: func(r *CreateMedicineRequest) { r.BrandID = uuid.Nil }, wantField: "brand_id"},
		{name: "missing category", mutate: func(r *CreateMedicineRequest) { r.CategoryID = uuid.Nil }, wantField: "category_id"},
		{
			name:      "blank barcode",
			mutate:    func(r *CreateMedicineRequest) { v := "  "; r.Barcode = &v },
			wantField: "barcode",
		},
		{
			name:      "invalid strength",
			mutate:    func(r *CreateMedicineRequest) { v := "strongish"; r.Strength = &v },
			wantField: "strength",
		},
		{
			name:      "units per pack zero",
			mutate:    func(r *CreateMedicineRequest) { v := 0; r.UnitsPerPack = &v },
			wantField: "units_per_pack",
		},
		{
			name:      "units per strip negative",
			mutate:    func(r *CreateMedicineRequest) { v := -1; r.UnitsPerStrip = &v },
			wantField: "units_per_strip",
		},
		{
			name:      "shelf life zero",
			mutate:    func(r *CreateMedicineRequest) { v := 0; r.ShelfLifeMonths = &v },
			wantField: "shelf_life_months",
		},
		{
			name:      "non http image url",
			mutate:    func(r *CreateMedicineRequest) { v := "ftp://example.com/a.png"; r.ImageURL = &v },
			wantField: "image_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validCreate()
			tt.mutate(&req)

			err := v.Struct(req)
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("expected the request to be valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a validation failure on %q", tt.wantField)
			}
			if !failingFields(t, err)[tt.wantField] {
				t.Fatalf("expected a failure on %q, got %v", tt.wantField, err)
			}
		})
	}
}

// TestCreateMedicineRequestValidatesInitialVariants: the inline variants are
// validated like standalone variant requests, and the failure names the
// offending element.
func TestCreateMedicineRequestValidatesInitialVariants(t *testing.T) {
	v := newMedicineTestValidator(t)

	req := validCreate()
	req.InitialVariants = []CreateMedicineVariantRequest{
		{Name: "10 tablets x 10 strips", SKU: "NAP-500-10"},
		{Name: "100 tablets", SKU: "NAP-500-100"},
	}
	if err := v.Struct(req); err != nil {
		t.Fatalf("valid initial variants must pass: %v", err)
	}

	req.InitialVariants = []CreateMedicineVariantRequest{
		{Name: "10 tablets x 10 strips", SKU: "NAP-500-10"},
		{Name: "", SKU: "NAP-500-100"},
	}
	err := v.Struct(req)
	if err == nil {
		t.Fatal("expected a validation failure on the second variant")
	}
	if !failingFields(t, err)["initial_variants[1].name"] {
		t.Fatalf("expected the failing element to be named, got %v", err)
	}
}

func TestCreateMedicineRequestAcceptsCommonStrengthNotations(t *testing.T) {
	v := newMedicineTestValidator(t)

	for _, strength := range []string{"500 mg", "5 mg/ml", "125 mg/5ml", "0.5 %", "100 IU", "500 mg-125 mg"} {
		req := validCreate()
		s := strength
		req.Strength = &s
		if err := v.Struct(req); err != nil {
			t.Errorf("strength %q should be accepted: %v", strength, err)
		}
	}
}

func TestUpdateMedicineRequestIsEmpty(t *testing.T) {
	if !(UpdateMedicineRequest{}).IsEmpty() {
		t.Fatal("an untouched update request must report IsEmpty")
	}

	// An explicit false is a change, not an omission: the pointer makes the
	// two distinguishable.
	disabled := false
	if (UpdateMedicineRequest{IsActive: &disabled}).IsEmpty() {
		t.Fatal("an explicit is_active=false must not report IsEmpty")
	}

	// A nil pointer field is an omission.
	if !(UpdateMedicineRequest{IsActive: nil}).IsEmpty() {
		t.Fatal("a nil pointer field must report IsEmpty")
	}

	if !(UpdateMedicineVariantRequest{}).IsEmpty() {
		t.Fatal("an untouched variant update request must report IsEmpty")
	}
	if (UpdateMedicineVariantRequest{IsActive: &disabled}).IsEmpty() {
		t.Fatal("an explicit variant is_active=false must not report IsEmpty")
	}
}

func TestLookupMedicineRequestRequiresExactlyOneKey(t *testing.T) {
	barcode := "8901234567890"
	sku := "NAP-500-10"

	tests := []struct {
		name    string
		request *LookupMedicineRequest
		wantErr bool
	}{
		{name: "nil request", request: nil, wantErr: true},
		{name: "neither", request: &LookupMedicineRequest{}, wantErr: true},
		{name: "both", request: &LookupMedicineRequest{Barcode: &barcode, SKU: &sku}, wantErr: true},
		{name: "blank barcode only", request: &LookupMedicineRequest{Barcode: &[]string{" "}[0]}, wantErr: true},
		{name: "barcode only", request: &LookupMedicineRequest{Barcode: &barcode}, wantErr: false},
		{name: "sku only", request: &LookupMedicineRequest{SKU: &sku}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.request.Validate()
			if tt.wantErr && len(errs) == 0 {
				t.Fatal("expected a validation failure")
			}
			if !tt.wantErr && len(errs) != 0 {
				t.Fatalf("expected the request to be valid, got %v", errs)
			}
		})
	}
}

func TestListMedicinesQueryNormalizeAndValidate(t *testing.T) {
	brandID := uuid.New()
	categoryID := uuid.New()
	genericID := uuid.New()

	t.Run("empty query is normalized", func(t *testing.T) {
		var q ListMedicinesQuery
		if errs := q.NormalizeAndValidate(); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
		if q.Page != 1 || q.Limit != 20 {
			t.Fatalf("expected the default pagination, got page=%d limit=%d", q.Page, q.Limit)
		}
		if q.BrandUUID() != nil || q.CategoryUUID() != nil || q.GenericUUID() != nil {
			t.Fatal("expected no UUID filters")
		}
	})

	t.Run("valid uuid filters are parsed", func(t *testing.T) {
		q := ListMedicinesQuery{
			BrandID:    brandID.String(),
			CategoryID: categoryID.String(),
			GenericID:  genericID.String(),
		}
		if errs := q.NormalizeAndValidate(); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
		if q.BrandUUID() == nil || *q.BrandUUID() != brandID {
			t.Fatalf("brand filter was not parsed: %v", q.BrandUUID())
		}
		if q.CategoryUUID() == nil || *q.CategoryUUID() != categoryID {
			t.Fatal("category filter was not parsed")
		}
		if q.GenericUUID() == nil || *q.GenericUUID() != genericID {
			t.Fatal("generic filter was not parsed")
		}
	})

	t.Run("malformed uuid filter is rejected", func(t *testing.T) {
		q := ListMedicinesQuery{BrandID: "not-a-uuid"}
		errs := q.NormalizeAndValidate()
		if len(errs) != 1 {
			t.Fatalf("expected exactly one error, got %v", errs)
		}
		var fieldErr *FieldError
		if !asFieldError(errs[0], &fieldErr) || fieldErr.Field != "brand_id" {
			t.Fatalf("expected a brand_id field error, got %v", errs[0])
		}
	})

	t.Run("unsupported sort field is rejected", func(t *testing.T) {
		var q ListMedicinesQuery
		q.Sort = "price"
		errs := q.NormalizeAndValidate()
		if len(errs) != 1 {
			t.Fatalf("expected exactly one error, got %v", errs)
		}
	})

	t.Run("supported sort field is accepted", func(t *testing.T) {
		var q ListMedicinesQuery
		q.Sort = "-name,sku"
		if errs := q.NormalizeAndValidate(); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
	})

	t.Run("over long search term is rejected", func(t *testing.T) {
		term := strings.Repeat("a", maxSearchLen+1)
		q := ListMedicinesQuery{Q: &term}
		errs := q.NormalizeAndValidate()
		if len(errs) != 1 {
			t.Fatalf("expected exactly one error, got %v", errs)
		}
	})

	t.Run("search term is trimmed", func(t *testing.T) {
		term := "  napa  "
		q := ListMedicinesQuery{Q: &term}
		if errs := q.NormalizeAndValidate(); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
		if q.NormalizedQ() != "napa" {
			t.Fatalf("expected the trimmed term, got %q", q.NormalizedQ())
		}
	})

	t.Run("pagination is clamped", func(t *testing.T) {
		q := ListMedicinesQuery{}
		q.Page = 0
		q.Limit = 5000
		if errs := q.NormalizeAndValidate(); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
		if q.Page != 1 {
			t.Fatalf("expected page 1, got %d", q.Page)
		}
		if q.Limit > 100 {
			t.Fatalf("expected the limit to be capped, got %d", q.Limit)
		}
	})
}

// TestQueryStructsPassStructValidation guards the query DTOs against the
// shared validator: they carry unexported parsed fields, and the handler
// binds them through httpx.BindQuery, which validates the whole struct.
func TestQueryStructsPassStructValidation(t *testing.T) {
	v := newMedicineTestValidator(t)

	if err := v.Struct(ListMedicinesQuery{}); err != nil {
		t.Fatalf("the list query must validate: %v", err)
	}
	if err := v.Struct(SearchMedicinesQuery{}); err != nil {
		t.Fatalf("the search query must validate: %v", err)
	}
}

func TestSearchMedicinesQueryRequiresTerm(t *testing.T) {
	var q SearchMedicinesQuery
	if errs := q.NormalizeAndValidate(); len(errs) != 1 {
		t.Fatalf("expected exactly one error for an empty term, got %v", errs)
	}

	term := "napa"
	q = SearchMedicinesQuery{Q: &term}
	if errs := q.NormalizeAndValidate(); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if q.NormalizedQ() != "napa" {
		t.Fatalf("expected the trimmed term, got %q", q.NormalizedQ())
	}
}

func TestCreateMedicineBarcodeRequestValidatesSymbology(t *testing.T) {
	v := newMedicineTestValidator(t)

	req := CreateMedicineBarcodeRequest{Barcode: "8901234567890"}
	if err := v.Struct(req); err != nil {
		t.Fatalf("expected the request to be valid, got %v", err)
	}

	req.BarcodeType = MedicineBarcodeType("EAN14")
	if err := v.Struct(req); err == nil {
		t.Fatal("expected an unsupported symbology to be rejected")
	}

	for _, typ := range []MedicineBarcodeType{
		MedicineBarcodeTypeEAN13,
		MedicineBarcodeTypeEAN8,
		MedicineBarcodeTypeUPC,
		MedicineBarcodeTypeCODE128,
		MedicineBarcodeTypeQR,
		MedicineBarcodeTypeOther,
	} {
		req.BarcodeType = typ
		if err := v.Struct(req); err != nil {
			t.Errorf("symbology %q should be accepted: %v", typ, err)
		}
	}
}

// jsonFieldNames returns the wire names a DTO exposes.
func jsonFieldNames(t *testing.T, value any) map[string]bool {
	t.Helper()
	rt := reflect.TypeOf(value)
	out := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

// asFieldError reports whether err is a *FieldError and binds it.
func asFieldError(err error, target **FieldError) bool {
	fe, ok := err.(*FieldError)
	if ok {
		*target = fe
	}
	return ok
}

func TestRequestDTOsExposeNoTenantAuditOrNonOwnedFacts(t *testing.T) {
	forbidden := []string{
		"id", "organization_id", "branch_id", "warehouse_id",
		"created_by", "updated_by", "created_at", "updated_at", "deleted_at",
		"mrp", "default_cost", "min_stock_threshold", "max_stock_threshold",
		"changed_by", "effective_from",
	}

	requests := []any{
		CreateMedicineRequest{},
		UpdateMedicineRequest{},
		CreateMedicineVariantRequest{},
		UpdateMedicineVariantRequest{},
		CreateMedicineBarcodeRequest{},
		LookupMedicineRequest{},
	}

	for _, req := range requests {
		names := jsonFieldNames(t, req)
		for _, field := range forbidden {
			if names[field] {
				t.Errorf("%T must not expose %q", req, field)
			}
		}
	}
}

func TestUpdateDTOsDoNotExposeIdentityKeys(t *testing.T) {
	// The exact-lookup keys and the tenant-scope carrier are write-once.
	for _, req := range []any{UpdateMedicineRequest{}, UpdateMedicineVariantRequest{}} {
		names := jsonFieldNames(t, req)
		for _, field := range []string{"sku", "barcode"} {
			if names[field] {
				t.Errorf("%T must not expose %q", req, field)
			}
		}
	}

	// brand_id carries the medicine's tenant scope.
	if jsonFieldNames(t, UpdateMedicineRequest{})["brand_id"] {
		t.Error("UpdateMedicineRequest must not expose brand_id")
	}
}

func TestMedicineResponseExcludesNonOwnedFacts(t *testing.T) {
	names := jsonFieldNames(t, MedicineResponse{})

	for _, field := range []string{"mrp", "default_cost", "min_stock_threshold", "max_stock_threshold"} {
		if names[field] {
			t.Errorf("MedicineResponse must not expose %q", field)
		}
	}

	for _, field := range []string{"id", "sku", "name", "brand_id", "category_id", "is_active"} {
		if !names[field] {
			t.Errorf("MedicineResponse must expose %q", field)
		}
	}
}
